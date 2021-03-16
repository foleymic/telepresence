// tun_linux.go: Open a Tunnel (L3 virtual interface) using the Universal TUN/TAP device driver.
package tun

import (
	"context"
	"net"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

const devicePath = "/dev/net/tun"
const bufferPrefixLen = 0

func OpenTun() (*Device, error) {
	// https://www.kernel.org/doc/Documentation/networking/tuntap.txt

	fd, err := unix.Open(devicePath, unix.O_RDWR, 0)
	if err != nil {
		return nil, err
	}

	var flagsRequest struct {
		name  [unix.IFNAMSIZ]byte
		flags int16
	}
	copy(flagsRequest.name[:], "tel%d")
	flagsRequest.flags = unix.IFF_TUN | unix.IFF_NO_PI

	err = unix.IoctlSetInt(fd, unix.TUNSETIFF, int(uintptr(unsafe.Pointer(&flagsRequest))))
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	var name string
	for i := 0; i < unix.IFNAMSIZ; i++ {
		if flagsRequest.name[i] == 0 {
			name = string(flagsRequest.name[0:i])
			break
		}
	}
	if name == "" {
		name = string(flagsRequest.name[:])
	}

	htons := func(value uint16) uint16 {
		test := uint16(1)
		if (*[2]byte)(unsafe.Pointer(&test))[0] == 1 {
			// this machine is little endian, swap bytes
			value = value&0xff<<8 | value&0xff00>>8
		}
		return value
	}

	// Passing a network ordered short here is required.
	provisioningSocket, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return nil, err
	}
	if err = unix.BindToDevice(provisioningSocket, name); err != nil {
		return nil, err
	}
	flagsRequest.flags |= unix.IFF_UP | unix.IFF_RUNNING
	if err = ioctl(provisioningSocket, unix.SIOCSIFFLAGS, unsafe.Pointer(&flagsRequest)); err != nil {
		return nil, err
	}

	index, err := getInterfaceIndex(fd, name)

	// Set non-blocking so that Read() doesn't hang for several seconds when the
	// fd is Closed. Read() will still wait for data to arrive.
	//
	// See: https://github.com/golang/go/issues/30426#issuecomment-470044803
	_ = unix.SetNonblock(fd, true)
	return &Device{File: os.NewFile(uintptr(fd), devicePath), name: name, index: uint32(index)}, nil
}

func (t *Device) AddSubnet(_ context.Context, subnet *net.IPNet, to net.IP) error {
	if err := t.setAddr(subnet, to); err != nil {
		return err
	}
	return withRouteSocket(func(s int) error {
		//		return t.routeAdd(s, subnet, to)
		return nil
	})
}

func (t *Device) setAddr(subnet *net.IPNet, to net.IP) error {
	if sub4, to4, ok := addrToIp4(subnet, to); ok {
		return withSocket(unix.AF_INET, func(fd int) error {
			var addressRequest struct {
				name [unix.IFNAMSIZ]byte
				addr unix.RawSockaddrInet4
			}
			copy(addressRequest.name[:], t.name)
			addressRequest.addr.Family = unix.AF_INET
			copy(addressRequest.addr.Addr[:], sub4.IP)
			err := unix.IoctlSetInt(fd, unix.SIOCSIFADDR, int(uintptr(unsafe.Pointer(&addressRequest))))
			if err != nil {
				return err
			}
			copy(addressRequest.addr.Addr[:], to4)
			err = unix.IoctlSetInt(fd, unix.SIOCSIFDSTADDR, int(uintptr(unsafe.Pointer(&addressRequest))))
			if err != nil {
				return err
			}
			copy(addressRequest.addr.Addr[:], sub4.Mask)
			err = unix.IoctlSetInt(fd, unix.SIOCSIFNETMASK, int(uintptr(unsafe.Pointer(&addressRequest))))
			runtime.KeepAlive(&addressRequest)
			return err
		})
	}
	return withSocket(unix.AF_INET6, func(fd int) error {
		// struct in6_ifreq in linux/ipv6.h
		var addressRequest struct {
			addr      [16]byte // struct in6_addr (u6_addr8[16]) in linux/in6.h
			prefixLen uint32
			index     uint32
		}
		copy(addressRequest.addr[:], subnet.IP)
		addressRequest.prefixLen = 64
		addressRequest.index = t.index
		err := ioctl(fd, unix.SIOCSIFADDR, unsafe.Pointer(&addressRequest))
		runtime.KeepAlive(&addressRequest)
		return err
	})
}

func (t *Device) Read(into *Buffer, n int) (int, error) {
	return t.File.Read(into.Raw()[:n])
}

func (t *Device) Write(from *Buffer) (int, error) {
	return t.File.Write(into.Raw())
}

func ioctl(socket int, request uint, requestData unsafe.Pointer) error {
	return unix.IoctlSetInt(socket, request, int(uintptr(requestData)))
}

func getInterfaceIndex(fd int, name string) (uint32, error) {
	var indexRequest struct {
		name  [unix.IFNAMSIZ]byte
		index uint32
	}
	copy(indexRequest.name[:], name)
	err := ioctl(fd, unix.SIOCGIFINDEX, unsafe.Pointer(&indexRequest))
	return indexRequest.index, err
}

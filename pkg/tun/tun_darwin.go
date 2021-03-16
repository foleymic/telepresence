package tun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const sysProtoControl = 2
const uTunOptIfName = 2
const uTunControlName = "com.apple.net.utun_control"

func OpenTun() (*Device, error) {
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, sysProtoControl)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = unix.Close(fd)
		}
	}()

	info := &unix.CtlInfo{}
	copy(info.Name[:], uTunControlName)
	if err = unix.IoctlCtlInfo(fd, info); err != nil {
		return nil, fmt.Errorf("failed to get IOCTL info: %w", err)
	}

	if err = unix.Connect(fd, &unix.SockaddrCtl{ID: info.Id, Unit: 0}); err != nil {
		return nil, err
	}

	if err = syscall.SetNonblock(fd, true); err != nil {
		return nil, err
	}

	name, err := unix.GetsockoptString(fd, sysProtoControl, uTunOptIfName)
	if err != nil {
		return nil, err
	}
	return &Device{os.NewFile(uintptr(fd), ""), name}, nil
}

func (t *Device) AddSubnet(_ context.Context, subnet *net.IPNet, to net.IP) error {
	if err := t.setAddr(subnet, to); err != nil {
		return err
	}
	return withRouteSocket(func(s int) error {
		return t.routeAdd(s, 1, subnet, to)
	})
}

func (t *Device) SetMTU(mtu int) error {
	return withSocket(unix.AF_INET, func(fd int) error {
		var ifr unix.IfreqMTU
		copy(ifr.Name[:], t.name)
		ifr.MTU = int32(mtu)
		err := unix.IoctlSetIfreqMTU(fd, &ifr)
		if err != nil {
			err = fmt.Errorf("set MTU on %s failed: %w", t.name, err)
		}
		return err
	})
}

var bufPool = sync.Pool{New: func() interface{} {
	return make([]byte, 0x1000)
}}

func (t *Device) Read(into []byte) (int, error) {
	buf := bufPool.Get().([]byte)
	if cap(buf) < len(into)+4 {
		buf = make([]byte, len(into)+4)
	}
	defer bufPool.Put(buf)
	rBuf := buf[:len(into)+4]
	n, err := t.File.Read(rBuf)
	if n < 4 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, err
	}
	copy(into, rBuf[4:])
	return n - 4, err
}

func (t *Device) Write(from []byte) (int, error) {
	if len(from) == 0 {
		return 0, syscall.EIO
	}

	buf := bufPool.Get().([]byte)
	if cap(buf) < len(from)+4 {
		buf = make([]byte, len(from)+4)
	}
	defer bufPool.Put(buf)

	wBuf := buf[:len(from)+4]
	ipVer := from[0] >> 4
	if ipVer == 4 {
		wBuf[3] = syscall.AF_INET
	} else if ipVer == 6 {
		wBuf[3] = syscall.AF_INET6
	} else {
		return 0, errors.New("unable to determine IP version from packet")
	}
	copy(wBuf[4:], from)
	n, err := t.File.Write(wBuf)
	return n - 4, err
}

// Address structure for the SIOCAIFADDR ioctlHandle request
//
// See https://www.unix.com/man-page/osx/4/netintro/
type addrIfReq struct {
	name [unix.IFNAMSIZ]byte
	addr unix.RawSockaddrInet4
	dest unix.RawSockaddrInet4
	mask unix.RawSockaddrInet4
}

// Address structure for the SIOCAIFADDR_IN6 ioctlHandle request
//
// See https://www.unix.com/man-page/osx/4/netintro/
type addrIfReq6 struct {
	name           [unix.IFNAMSIZ]byte
	addr           unix.RawSockaddrInet6
	dest           unix.RawSockaddrInet6
	mask           unix.RawSockaddrInet6
	flags          int32
	expire         int64
	preferred      int64
	validLifeTime  uint32
	prefixLifeTime uint32
}

// SIOCAIFADDR_IN6 is the same ioctlHandle identifier as unix.SIOCAIFADDR adjusted with size of addrIfReq6
const SIOCAIFADDR_IN6 = (unix.SIOCAIFADDR & 0xe000ffff) | (uint(unsafe.Sizeof(addrIfReq6{})) << 16)
const ND6_INFINITE_LIFETIME = 0xffffffff

func (t *Device) setAddr(subnet *net.IPNet, to net.IP) error {
	if sub4, to4, ok := addrToIp4(subnet, to); ok {
		return withSocket(unix.AF_INET, func(fd int) error {
			ifreq := &addrIfReq{
				addr: unix.RawSockaddrInet4{Len: 16, Family: unix.AF_INET},
				dest: unix.RawSockaddrInet4{Len: 16, Family: unix.AF_INET},
				mask: unix.RawSockaddrInet4{Len: 16, Family: unix.AF_INET},
			}
			copy(ifreq.name[:], t.name)
			copy(ifreq.addr.Addr[:], sub4.IP)
			copy(ifreq.mask.Addr[:], sub4.Mask)
			copy(ifreq.dest.Addr[:], to4)
			err := unix.IoctlSetInt(fd, unix.SIOCAIFADDR, int(uintptr(unsafe.Pointer(ifreq))))
			runtime.KeepAlive(ifreq)
			return err
		})
	} else {
		return withSocket(unix.AF_INET6, func(fd int) error {
			ifreq := &addrIfReq6{
				addr:           unix.RawSockaddrInet6{Len: 28, Family: unix.AF_INET6},
				dest:           unix.RawSockaddrInet6{Len: 28, Family: unix.AF_INET6},
				mask:           unix.RawSockaddrInet6{Len: 28, Family: unix.AF_INET6},
				validLifeTime:  ND6_INFINITE_LIFETIME,
				prefixLifeTime: ND6_INFINITE_LIFETIME,
			}
			copy(ifreq.name[:], t.name)
			copy(ifreq.addr.Addr[:], subnet.IP.To16())
			copy(ifreq.mask.Addr[:], subnet.Mask)
			copy(ifreq.dest.Addr[:], to.To16())
			err := unix.IoctlSetInt(fd, SIOCAIFADDR_IN6, int(uintptr(unsafe.Pointer(ifreq))))
			runtime.KeepAlive(ifreq)
			return err
		})
	}
}

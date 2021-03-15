package tun

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/net/route"

	"golang.org/x/sys/unix"
)

const sysProtoControl = 2
const uTunOptIfName = 2
const uTunControlName = "com.apple.net.utun_control"

type tunDevice struct {
	*os.File
	name string
}

func (t *tunDevice) AddSubnet(_ context.Context, subnet *net.IPNet, to net.IP) error {
	if err := t.setAddr(subnet, to); err != nil {
		return err
	}
	return withRouteSocket(func(s int) error {
		return t.routeAdd(s, 1, subnet, to)
	})
}

// withRouteSocket will open the socket to where RouteMessages should be sent
// and call the given function with that socket. The socket is closed when the
// function returns
func withRouteSocket(f func(routeSocket int) error) error {
	routeSocket, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, syscall.AF_UNSPEC)
	if err != nil {
		return err
	}

	// Avoid the overhead of echoing messages back to sender
	if err = syscall.SetsockoptInt(routeSocket, syscall.SOL_SOCKET, syscall.SO_USELOOPBACK, 0); err != nil {
		return err
	}
	defer syscall.Close(routeSocket)
	return f(routeSocket)
}

// toRouteAddr converts an net.IP to its corresponding route.Addr
func toRouteAddr(ip net.IP) (addr route.Addr) {
	if ip4 := ip.To4(); ip4 != nil {
		dst := route.Inet4Addr{}
		copy(dst.IP[:], ip4)
		addr = &dst
	} else {
		dst := route.Inet6Addr{}
		copy(dst.IP[:], ip)
		addr = &dst
	}
	return addr
}

func toRouteMask(mask net.IPMask) (addr route.Addr) {
	if _, bits := mask.Size(); bits == 32 {
		dst := route.Inet4Addr{}
		copy(dst.IP[:], mask)
		addr = &dst
	} else {
		dst := route.Inet6Addr{}
		copy(dst.IP[:], mask)
		addr = &dst
	}
	return addr
}

func (t *tunDevice) newRouteMessage(rtm, seq int, subnet *net.IPNet, gw net.IP) *route.RouteMessage {
	return &route.RouteMessage{
		Version: syscall.RTM_VERSION,
		ID:      uintptr(os.Getpid()),
		Seq:     seq,
		Type:    rtm,
		Flags:   syscall.RTF_UP | syscall.RTF_STATIC | syscall.RTF_CLONING,
		Addrs: []route.Addr{
			syscall.RTAX_DST:     toRouteAddr(subnet.IP),
			syscall.RTAX_GATEWAY: toRouteAddr(gw),
			syscall.RTAX_NETMASK: toRouteMask(subnet.Mask),
		},
	}
}

func (t *tunDevice) routeAdd(routeSocket, seq int, r *net.IPNet, gw net.IP) error {
	m := t.newRouteMessage(syscall.RTM_ADD, seq, r, gw)
	wb, err := m.Marshal()
	if err != nil {
		return err
	}
	_, err = syscall.Write(routeSocket, wb)
	if err == unix.EEXIST {
		// route exists, that's OK
		err = nil
	}
	return err
}

func (t *tunDevice) routeClear(routeSocket, seq int, r *net.IPNet, gw net.IP) error {
	m := t.newRouteMessage(syscall.RTM_DELETE, seq, r, gw)
	wb, err := m.Marshal()
	if err != nil {
		return err
	}
	_, err = syscall.Write(routeSocket, wb)
	if err == unix.ESRCH {
		// route doesn't exist, that's OK
		err = nil
	}
	return err
}

func OpenTun() (*tunDevice, error) {
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
	return &tunDevice{os.NewFile(uintptr(fd), ""), name}, nil
}

func (t *tunDevice) Name() string {
	return t.name
}

func withSocket(domain int, f func(fd int) error) error {
	fd, err := unix.Socket(domain, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return f(fd)
}

func addrToIp4(subnet *net.IPNet, to net.IP) (*net.IPNet, net.IP, bool) {
	if to4 := to.To4(); to4 != nil {
		if dest4 := subnet.IP.To4(); dest4 != nil {
			if _, bits := subnet.Mask.Size(); bits == 32 {
				return &net.IPNet{IP: dest4, Mask: subnet.Mask}, to4, true
			}
		}
	}
	return nil, nil, false
}

// Address structure for the SIOCAIFADDR ioctl request
//
// See https://www.unix.com/man-page/osx/4/netintro/
type addrIfReq struct {
	name [unix.IFNAMSIZ]byte
	addr unix.RawSockaddrInet4
	dest unix.RawSockaddrInet4
	mask unix.RawSockaddrInet4
}

// Address structure for the SIOCAIFADDR_IN6 ioctl request
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

// SIOCAIFADDR_IN6 is the same ioctl identifier as unix.SIOCAIFADDR adjusted with size of addrIfReq6
const SIOCAIFADDR_IN6 = (unix.SIOCAIFADDR & 0xe000ffff) | (uint(unsafe.Sizeof(addrIfReq6{})) << 16)
const ND6_INFINITE_LIFETIME = 0xffffffff

func (t *tunDevice) setAddr(subnet *net.IPNet, to net.IP) error {
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

func (t *tunDevice) SetMTU(mtu int) error {
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

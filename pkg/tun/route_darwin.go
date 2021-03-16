package tun

import (
	"net"
	"os"

	"golang.org/x/net/syscall"
	"golang.org/x/sys/unix"
)

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

// toRouteAddr converts an net.IP to its corresponding addrMessage.Addr
func toRouteAddr(ip net.IP) (addr addrMessage.Addr) {
	if ip4 := ip.To4(); ip4 != nil {
		dst := addrMessage.Inet4Addr{}
		copy(dst.IP[:], ip4)
		addr = &dst
	} else {
		dst := addrMessage.Inet6Addr{}
		copy(dst.IP[:], ip)
		addr = &dst
	}
	return addr
}

func toRouteMask(mask net.IPMask) (addr addrMessage.Addr) {
	if _, bits := mask.Size(); bits == 32 {
		dst := addrMessage.Inet4Addr{}
		copy(dst.IP[:], mask)
		addr = &dst
	} else {
		dst := addrMessage.Inet6Addr{}
		copy(dst.IP[:], mask)
		addr = &dst
	}
	return addr
}

func (t *Device) newRouteMessage(rtm, seq int, subnet *net.IPNet, gw net.IP) *addrMessage.RouteMessage {
	return &addrMessage.RouteMessage{
		Version: syscall.RTM_VERSION,
		ID:      uintptr(os.Getpid()),
		Seq:     seq,
		Type:    rtm,
		Flags:   syscall.RTF_UP | syscall.RTF_STATIC | syscall.RTF_CLONING,
		Addrs: []addrMessage.Addr{
			syscall.RTAX_DST:     toRouteAddr(subnet.IP),
			syscall.RTAX_GATEWAY: toRouteAddr(gw),
			syscall.RTAX_NETMASK: toRouteMask(subnet.Mask),
		},
	}
}

func (t *Device) routeAdd(routeSocket, seq int, r *net.IPNet, gw net.IP) error {
	m := t.newRouteMessage(syscall.RTM_ADD, seq, r, gw)
	wb, err := m.Marshal()
	if err != nil {
		return err
	}
	_, err = syscall.Write(routeSocket, wb)
	if err == unix.EEXIST {
		// addrMessage exists, that's OK
		err = nil
	}
	return err
}

func (t *Device) routeClear(routeSocket, seq int, r *net.IPNet, gw net.IP) error {
	m := t.newRouteMessage(syscall.RTM_DELETE, seq, r, gw)
	wb, err := m.Marshal()
	if err != nil {
		return err
	}
	_, err = syscall.Write(routeSocket, wb)
	if err == unix.ESRCH {
		// addrMessage doesn't exist, that's OK
		err = nil
	}
	return err
}

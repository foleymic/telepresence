package tun

import (
	"bytes"
	"net"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// withRouteSocket will open the socket to where RouteMessages should be sent
// and call the given function with that socket. The socket is closed when the
// function returns
func withRouteSocket(f func(routeSocket int) error) error {
	routeSocket, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_ROUTE)
	if err != nil {
		return err
	}
	defer unix.Close(routeSocket)
	return f(routeSocket)
}

type addrMessage struct {
	family    uint8
	prefixLen uint8
	flags     uint8
	scope     uint8
	index     uint32
}

func (t *Device) routeAdd(routeSocket int, network *net.IPNet, gateway net.IP) error {
	header := unix.NlMsghdr{
		Len:   uint32(unix.SizeofNlMsghdr),
		Type:  uint16(unix.RTM_NEWADDR),
		Flags: uint16(unix.NLM_F_REQUEST | unix.NLM_F_CREATE | unix.NLM_F_EXCL),
	}

	var family uint8
	if sub4, to4, ok := addrToIp4(network, gateway); ok {
		network = sub4
		gateway = to4
		family = unix.AF_INET
	} else {
		family = unix.AF_INET6
	}
	ones, _ := network.Mask.Size()
	msg := addrMessage{
		family:    family,
		prefixLen: uint8(ones),
		index:     t.index,
	}

	attrs := []syscall.NetlinkRouteAttr{
		{
			Attr: syscall.RtAttr{
				Len:  uint16(len(network.IP)),
				Type: unix.IFA_BROADCAST,
			},
			Value: network.IP,
		},
		{
			Attr: syscall.RtAttr{
				Len:  uint16(len(gateway)),
				Type: unix.IFA_ADDRESS,
			},
			Value: gateway,
		},
	}

	headerBytes := *(*[unix.SizeofNlMsghdr]byte)(unsafe.Pointer(&header))

	bf := bytes.Buffer{}
	bf.Write(headerBytes[:])

	msgBytes := *(*[unsafe.Sizeof(msg)]byte)(unsafe.Pointer(&msg))
	bf.Write(msgBytes[:])

	for _, attr := range attrs {
		attrBytes := *(*[unsafe.Sizeof(attr.Attr)]byte)(unsafe.Pointer(&attr.Attr))
		bf.Write(attrBytes[:])
		bf.Write(attr.Value) // no alignment necessary, we know it's either 4 or 16 bytes
	}

	return unix.Sendto(routeSocket, bf.Bytes(), 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK})
}

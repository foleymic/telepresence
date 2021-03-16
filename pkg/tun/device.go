package tun

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

type Device struct {
	*os.File
	name  string
	index uint32
}

func (t *Device) Name() string {
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

// tun_linux.go: Open a Tunnel (L3 virtual interface) using the Universal TUN/TAP device driver.
package tun

import (
	"os"

	"golang.org/x/sys/unix"
)

const devicePath = "/dev/net/tun"

func OpenTun() (*Device, error) {
	// https://www.kernel.org/doc/Documentation/networking/tuntap.txt

	fd, err := unix.Open(devicePath, unix.O_RDWR, 0)
	if err != nil {
		return nil, "", err
	}

	name, err := IoctlTunSetInterfaceFlags(fd, "tel%d", unix.IFF_TUN|unix.IFF_NO_PI)
	if err != nil {
		_ = unix.Close(fd)
		return nil, "", err
	}

	// Set non-blocking so that Read() doesn't hang for several seconds when the
	// fd is Closed. Read() will still wait for data to arrive.
	//
	// See: https://github.com/golang/go/issues/30426#issuecomment-470044803
	_ = unix.SetNonblock(fd, true)
	return &Device{File: os.NewFile(uintptr(fd), devicePath), name: name}, nil
}

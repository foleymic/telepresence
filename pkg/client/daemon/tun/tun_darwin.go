package tun

import (
	"fmt"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const sysProtoControl = 2
const uTunOptIfName = 2
const uTunControlName = "com.apple.net.utun_control"

func OpenTun() (io.ReadWriteCloser, string, error) {
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, sysProtoControl)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if err != nil {
			_ = unix.Close(fd)
		}
	}()

	info := &unix.CtlInfo{}
	copy(info.Name[:], uTunControlName)
	if err = unix.IoctlCtlInfo(fd, info); err != nil {
		return nil, "", fmt.Errorf("failed to get IOCTL info: %w", err)
	}

	if err = unix.Connect(fd, &unix.SockaddrCtl{ID: info.Id, Unit: 0}); err != nil {
		return nil, "", err
	}

	if err = syscall.SetNonblock(fd, true); err != nil {
		return nil, "", err
	}

	name, err := unix.GetsockoptString(fd, sysProtoControl, uTunOptIfName)
	if err != nil {
		return nil, "", err
	}
	return os.NewFile(uintptr(fd), ""), name, nil
}

func SetMTU(ifName string, mtu int) error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	var ifr unix.IfreqMTU
	copy(ifr.Name[:], ifName)
	ifr.MTU = int32(mtu)
	err = unix.IoctlSetIfreqMTU(fd, &ifr)
	if err != nil {
		return fmt.Errorf("set MTU on %s failed: %w", ifName, err)
	}
	return nil
}

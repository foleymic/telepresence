package tun

import "os"

type Device struct {
	*os.File
	name string
}

func (t *Device) Name() string {
	return t.name
}

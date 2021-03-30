package tun

import (
	"net"
	"os"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/buffer"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"

	"golang.org/x/net/ipv4"

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

func (d *Device) sendIPv4Fragments(b *buffer.Data, l4HeaderOffset int, payload []byte) error {
	payloadLen := len(payload) + l4HeaderOffset

	h := ip.V4Header(b.Buf())

	// maxPayLoadLen must be on even 8 byte boundary
	maxPayloadLen := buffer.DataPool.MTU - h.HeaderLen()
	maxPayloadLen -= maxPayloadLen % 8

	if payloadLen <= maxPayloadLen {
		// no fragments needed.
		copy(h.Payload()[l4HeaderOffset:], payload)
		h.SetPayloadLen(h.HeaderLen() + payloadLen)
		h.SetChecksum()
		_, err := d.Write(b)
		return err
	}

	payloadLen = maxPayloadLen - l4HeaderOffset
	copy(h.Payload()[l4HeaderOffset:], payload[:payloadLen])

	h.SetPayloadLen(h.HeaderLen() + maxPayloadLen)
	h.SetFlags(ipv4.MoreFragments)
	h.SetChecksum()
	if _, err := d.Write(b.Slice(0, h.HeaderLen()+h.PayloadLen())); err != nil {
		return err
	}

	fragmentOffset := maxPayloadLen
	for {
		payload = payload[payloadLen:]
		payloadLen = len(payload)
		lastFragment := payloadLen <= maxPayloadLen
		if !lastFragment {
			h.SetFlags(ipv4.MoreFragments)
			payloadLen = maxPayloadLen
		}
		h.SetPayloadLen(h.HeaderLen() + payloadLen)
		copy(h.Payload(), payload[:payloadLen])

		h.SetFragmentOffset(fragmentOffset / 8)
		h.SetChecksum()
		if _, err := d.Write(b.Slice(0, h.HeaderLen()+h.PayloadLen())); err != nil {
			return err
		}
		if lastFragment {
			return nil
		}
		fragmentOffset += maxPayloadLen
	}
}

package ip

import (
	"encoding/binary"
	"fmt"
	"net"
)

type ConnID string

func NewConnID(iph Header, srcPort, dstPort uint16) ConnID {
	src := iph.Source()
	dst := iph.Destination()
	ls := len(src)
	ld := len(dst)
	bs := make([]byte, ls+ld+4)
	copy(bs, src)
	binary.BigEndian.PutUint16(bs[ls:], srcPort)
	ls += 2
	copy(bs[ls:], dst)
	ls += ld
	binary.BigEndian.PutUint16(bs[ls:], dstPort)
	return ConnID(bs)
}

func (id ConnID) IsIPv4() bool {
	return len(id) == 12
}

func (id ConnID) Source() net.IP {
	if id.IsIPv4() {
		return net.IP(id[0:4])
	}
	return net.IP(id[0:16])
}

func (id ConnID) SourcePort() uint16 {
	if id.IsIPv4() {
		return binary.BigEndian.Uint16([]byte(id)[4:])
	}
	return binary.BigEndian.Uint16([]byte(id)[16:])
}

func (id ConnID) Destination() net.IP {
	if id.IsIPv4() {
		return net.IP(id[6:10])
	}
	return net.IP(id[18:34])
}

func (id ConnID) DestinationPort() uint16 {
	if id.IsIPv4() {
		return binary.BigEndian.Uint16([]byte(id)[10:])
	}
	return binary.BigEndian.Uint16([]byte(id)[34:])
}

func (id ConnID) Network() string {
	if id.IsIPv4() {
		return "ip4"
	}
	return "ip6"
}

func (id ConnID) String() string {
	return fmt.Sprintf("%s:%d -> %s:%d", id.Source(), id.SourcePort(), id.Destination(), id.DestinationPort())
}

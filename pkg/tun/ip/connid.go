package ip

import (
	"encoding/binary"
	"fmt"
	"net"
)

// A ConnID is a compact and immutable representation of source IP, source port, destination IP and destination port which
// is suitable as a map key.
type ConnID string

// NewConnID returns a new ConnID for the given values.
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

// ISIPv4 returns true if the source and destination of this ConnID are IPv4
func (id ConnID) IsIPv4() bool {
	return len(id) == 12
}

// Source returns the source IP
func (id ConnID) Source() net.IP {
	if id.IsIPv4() {
		return net.IP(id[0:4])
	}
	return net.IP(id[0:16])
}

// SourcePort returns the source port
func (id ConnID) SourcePort() uint16 {
	if id.IsIPv4() {
		return binary.BigEndian.Uint16([]byte(id)[4:])
	}
	return binary.BigEndian.Uint16([]byte(id)[16:])
}

// Destination returns the destination IP
func (id ConnID) Destination() net.IP {
	if id.IsIPv4() {
		return net.IP(id[6:10])
	}
	return net.IP(id[18:34])
}

// DestinationPort returns the destination port
func (id ConnID) DestinationPort() uint16 {
	if id.IsIPv4() {
		return binary.BigEndian.Uint16([]byte(id)[10:])
	}
	return binary.BigEndian.Uint16([]byte(id)[34:])
}

// Network returns either "ip4" or "ip6"
func (id ConnID) Network() string {
	if id.IsIPv4() {
		return "ip4"
	}
	return "ip6"
}

// String returns a formatted string suitable for logging showing the source:sourcePort -> destination:destinationPort
func (id ConnID) String() string {
	return fmt.Sprintf("%s:%d -> %s:%d", id.Source(), id.SourcePort(), id.Destination(), id.DestinationPort())
}

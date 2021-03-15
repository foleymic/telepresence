package tun

import (
	"encoding/binary"
	"fmt"
	"net"
)

const (
	// I use TUN interface, so only plain IP packet, no ethernet header + mtu is set to 1300
	bufSize      = 1500
	mtu          = "1300"
	udpProto     = 0x11
	udpHeaderLen = 8
)

// The UDP datagram and its payload
type udpDatagram []byte

func (u udpDatagram) setValues(src, dst uint16, body []byte) {
	binary.BigEndian.PutUint16(u, src)
	binary.BigEndian.PutUint16(u[2:], dst)
	binary.BigEndian.PutUint16(u[4:], uint16(udpHeaderLen+len(body)))
	copy(u[udpHeaderLen:], body)
}

func (u udpDatagram) source() uint16 {
	return binary.BigEndian.Uint16(u)
}

func (u udpDatagram) destination() uint16 {
	return binary.BigEndian.Uint16(u[2:])
}

func (u udpDatagram) length() uint16 {
	return binary.BigEndian.Uint16(u[4:])
}

func (u udpDatagram) checksum() uint16 {
	return binary.BigEndian.Uint16(u[6:])
}

func (u udpDatagram) setChecksum(src, dst net.IP, proto byte) {
	// reset current checksum, if any
	binary.BigEndian.PutUint16(u[6:], 0)

	buf := make([]byte, 12+len(u))

	// Write a pseudo-header with src IP, dst IP, 0, protocol, and datagram length
	copy(buf, src)
	copy(buf[4:], dst)
	buf[8] = 0
	buf[9] = proto
	binary.BigEndian.PutUint16(buf[10:], u.length())
	copy(buf[12:], u)

	// compute and assign checksum
	binary.BigEndian.PutUint16(u[6:], checksum(buf))
}

func (u udpDatagram) body() []byte {
	return u[udpHeaderLen:]
}

func (u udpDatagram) String() string {
	return fmt.Sprintf("src=%d, dst=%d, length=%d, crc=%d", u.source(), u.destination(), u.length()-udpHeaderLen, u.checksum())
}

func checksum(buf []byte) uint16 {
	s := uint32(0)
	t := len(buf)
	if (t % 2) != 0 {
		// uneven length, add last byte << 8
		t--
		s = uint32(buf[t]) << 8
	}
	for i := 0; i < t; i += 2 {
		s += uint32(buf[i])<<8 | uint32(buf[i+1])
	}
	for s > 0xffff {
		s = (s >> 16) + (s & 0xffff)
	}
	c := ^uint16(s)

	if c == 0 {
		// From RFC 768: If the computed checksum is zero, it is transmitted as all ones.
		c = 0xffff
	}
	return c
}

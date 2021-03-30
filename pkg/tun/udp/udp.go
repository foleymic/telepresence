package udp

import (
	"encoding/binary"
	"fmt"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"

	"golang.org/x/sys/unix"
)

const HeaderLen = 8

// The UDP datagram and its payload
type Header []byte

func (u Header) SetValues(src, dst uint16, payloadLen int) {
	binary.BigEndian.PutUint16(u, src)
	binary.BigEndian.PutUint16(u[2:], dst)
	binary.BigEndian.PutUint16(u[4:], uint16(HeaderLen+payloadLen))
}

func (u Header) SourcePort() int {
	return int(binary.BigEndian.Uint16(u))
}

func (u Header) DestinationPort() int {
	return int(binary.BigEndian.Uint16(u[2:]))
}

func (u Header) PayloadLength() int {
	return int(binary.BigEndian.Uint16(u[4:]) - HeaderLen)
}

func (u Header) Checksum() uint16 {
	return binary.BigEndian.Uint16(u[6:])
}

func (u Header) SetChecksum(ipHdr ip.Header) {
	ip.L4Checksum(ipHdr, 6, unix.IPPROTO_UDP)
}

func (u Header) Payload() []byte {
	return u[HeaderLen:]
}

func (u Header) String() string {
	return fmt.Sprintf("src=%d, dst=%d, length=%d, crc=%d", u.SourcePort(), u.DestinationPort(), u.PayloadLength()-HeaderLen, u.Checksum())
}

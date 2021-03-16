package tcp

import (
	"bytes"
	"encoding/binary"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"

	"golang.org/x/sys/unix"
)

// HeaderLen is the length of a TCP header that doesn't have any options
const HeaderLen = 20
const HeaderMaxLen = 60

// The TCP header and its payload
type Header []byte

func (h Header) SourcePort() uint16 {
	return binary.BigEndian.Uint16(h)
}

func (h Header) SetSourcePort(port uint16) {
	binary.BigEndian.PutUint16(h, port)
}

func (h Header) DestinationPort() uint16 {
	return binary.BigEndian.Uint16(h[2:])
}

func (h Header) SetDestinationPort(port uint16) {
	binary.BigEndian.PutUint16(h[2:], port)
}

func (h Header) Sequence() uint32 {
	return binary.BigEndian.Uint32(h[4:])
}

func (h Header) SetSequence(sq uint32) {
	binary.BigEndian.PutUint32(h[4:], sq)
}

func (h Header) AckNumber() uint32 {
	return binary.BigEndian.Uint32(h[8:])
}

func (h Header) SetAckNumber(aq uint32) {
	binary.BigEndian.PutUint32(h[8:], aq)
}

func (h Header) DataOffset() int {
	return int(h[12] >> 4)
}

func (h Header) SetDataOffset(offset int) {
	h[12] = (h[12] & 0x0f) | uint8(offset<<4)
}

func (h Header) NS() bool {
	return h[12]&0b00000001 != 0
}

func (h Header) SetNS(flag bool) {
	if flag {
		h[12] |= 0b00000001
	} else {
		h[12] &^= 0b00000001
	}
}

func (h Header) CWR() bool {
	return h[13]&0b10000000 != 0
}

func (h Header) SetCWR(flag bool) {
	if flag {
		h[13] |= 0b10000000
	} else {
		h[13] &^= 0b10000000
	}
}

func (h Header) ECE() bool {
	return h[13]&0b01000000 != 0
}

func (h Header) SetECE(flag bool) {
	if flag {
		h[13] |= 0b01000000
	} else {
		h[13] &^= 0b01000000
	}
}

func (h Header) URG() bool {
	return h[13]&0b00100000 != 0
}

func (h Header) SetURG(flag bool) {
	if flag {
		h[13] |= 0b00100000
	} else {
		h[13] &^= 0b00100000
	}
}

func (h Header) ACK() bool {
	return h[13]&0b00010000 != 0
}

func (h Header) SetACK(flag bool) {
	if flag {
		h[13] |= 0b00010000
	} else {
		h[13] &^= 0b00010000
	}
}

func (h Header) PSH() bool {
	return h[13]&0b00001000 != 0
}

func (h Header) SetPSH(flag bool) {
	if flag {
		h[13] |= 0b00001000
	} else {
		h[13] &^= 0b00001000
	}
}

func (h Header) RST() bool {
	return h[13]&0b00000100 != 0
}

func (h Header) SetRST(flag bool) {
	if flag {
		h[13] |= 0b00000100
	} else {
		h[13] &^= 0b00000100
	}
}

func (h Header) SYN() bool {
	return h[13]&0b00000010 != 0
}

func (h Header) SetSYN(flag bool) {
	if flag {
		h[13] |= 0b00000010
	} else {
		h[13] &^= 0b00000010
	}
}

func (h Header) FIN() bool {
	return h[13]&0b00000001 != 0
}

func (h Header) SetFIN(flag bool) {
	if flag {
		h[13] |= 0b00000001
	} else {
		h[13] &^= 0b00000001
	}
}

func (h Header) WindowSize() uint16 {
	return binary.BigEndian.Uint16(h[14:])
}

func (h Header) SetWindowSize(sz uint16) {
	binary.BigEndian.PutUint16(h[14:], sz)
}

func (h Header) Checksum() uint16 {
	return binary.BigEndian.Uint16(h[16:])
}

func (h Header) UrgentPointer() uint16 {
	return binary.BigEndian.Uint16(h[18:])
}

func (h Header) SetUrgentPointer(up uint16) {
	binary.BigEndian.PutUint16(h[18:], up)
}

func (h Header) OptionBytes() []byte {
	return h[20 : h.DataOffset()*4]
}

func (h Header) Payload() []byte {
	return h[h.DataOffset()*4:]
}

func (h Header) SetChecksum(ipHdr ip.Header) {
	ip.L4Checksum(ipHdr, 16, unix.IPPROTO_TCP)
}

func (h Header) AppendFlags(b *bytes.Buffer) {
	l := b.Len()
	if h.SYN() {
		b.WriteString("SYN,")
	}
	if h.RST() {
		b.WriteString("RST,")
	}
	if h.FIN() {
		b.WriteString("FIN,")
	}
	if h.ACK() {
		b.WriteString("ACK,")
	}
	if h.PSH() {
		b.WriteString("PSH,")
	}
	if h.URG() {
		b.WriteString("URG,")
	}
	if h.ECE() {
		b.WriteString("ECE,")
	}
	if h.CWR() {
		b.WriteString("CWR,")
	}
	if b.Len() > l {
		b.Truncate(b.Len() - 1)
	}
}

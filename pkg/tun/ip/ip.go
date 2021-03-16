package ip

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/buf"
)

type Header interface {
	Initialize()

	Version() int

	Destination() net.IP

	Source() net.IP

	// 	HeaderLength() is the length of this header
	HeaderLen() int

	// TotalLen is the total length of the packet which is the length of this header plus the length of the payload
	TotalLen() int

	// L4Protocol is the protocol of the layer-4 header.
	L4Protocol() int

	// Packet returns the full packet, i.e. both the header and the payload
	Packet() []byte

	// Payload returns the payload of the package that this is the header for
	Payload() []byte

	// PseudoHeader returns the pseudo header. All fields must be filled in before requesting this header.
	PseudoHeader(l4Proto int) []byte

	// SetTTL sets the hop limit
	SetTTL(id int)

	// SetSource sets the package source IP address
	SetSource(ip net.IP)

	// SetDestination sets the package destination IP address
	SetDestination(ip net.IP)

	// SetL4Protocol sets the layer 4 protocol
	SetL4Protocol(int)

	// SetTotalLen sets the total length (header + payload)
	SetTotalLen(int)

	// SetChecksum computes the checksum for this header. No further modifications must be made once this is called.
	// This method is a no-op for ipv6.
	SetChecksum()
}

type V4Header []byte

type V4Option []byte

func (o V4Option) Copied() bool {
	return o[0]&0x80 == 0x80
}

func (o V4Option) Class() int {
	return int(o[0]&0x60) >> 5
}

func (o V4Option) Number() int {
	return int(o[0] & 0x1f)
}

func (o V4Option) Len() int {
	if o.Number() > 1 {
		return int(o[1])
	}
	return 1
}

func (o V4Option) Data() []byte {
	if o.Number() > 1 {
		return o[2:o.Len()]
	}
	return nil
}

func (h V4Header) Initialize() {
	for i := len(h) - 1; i > 0; i-- {
		h[i] = 0
	}
	h[0] = ipv4.Version<<4 | ipv4.HeaderLen/4
}

func (h V4Header) Version() int {
	return int(h[0] >> 4)
}

func (h V4Header) HeaderLen() int {
	return int(h[0]&0x0f) * 4
}

func (h V4Header) SetHeaderLen(hl int) {
	h[0] = (h[0] & 0xf0) | uint8(hl/4)
}

func (h V4Header) DSCP() int {
	return int(h[1] >> 2)
}

func (h V4Header) ECN() int {
	return int(h[1] & 0x3)
}

func (h V4Header) TotalLen() int {
	return int(binary.BigEndian.Uint16(h[2:]))
}

func (h V4Header) SetTotalLen(len int) {
	binary.BigEndian.PutUint16(h[2:], uint16(len))
}

func (h V4Header) ID() uint16 {
	return binary.BigEndian.Uint16(h[4:])
}

func (h V4Header) SetID(id int) {
	binary.BigEndian.PutUint16(h[4:], uint16(id))
}

func (h V4Header) Flags() ipv4.HeaderFlags {
	return ipv4.HeaderFlags(h[6]) >> 5
}

func (h V4Header) SetFlags(flags ipv4.HeaderFlags) {
	h[6] = (h[6] & 0x1f) | uint8(flags<<5)
}

func (h V4Header) FragmentOffset() int {
	return int(binary.BigEndian.Uint16(h[6:]) & 0x1fff)
}

func (h V4Header) SetFragmentOffset(fragOff int) {
	flagBits := uint16(h[6]&0b11100000) << 8
	binary.BigEndian.PutUint16(h[6:], flagBits|uint16(fragOff&0x1fff))
}

func (h V4Header) TTL() int {
	return int(h[8])
}

func (h V4Header) SetTTL(hops int) {
	h[8] = uint8(hops)
}

func (h V4Header) L4Protocol() int {
	return int(h[9])
}

func (h V4Header) SetL4Protocol(proto int) {
	h[9] = uint8(proto)
}

func (h V4Header) Checksum() int {
	return int(binary.BigEndian.Uint16(h[10:]))
}

func (h V4Header) Source() net.IP {
	return net.IP(h[12:16])
}

func (h V4Header) Destination() net.IP {
	return net.IP(h[16:20])
}

func (h V4Header) SetSource(ip net.IP) {
	if ip4 := ip.To4(); ip4 != nil {
		copy(h[12:], ip)
	}
}

func (h V4Header) SetDestination(ip net.IP) {
	if ip4 := ip.To4(); ip4 != nil {
		copy(h[16:20], ip)
	}
}

func (h V4Header) Options() ([]V4Option, error) {
	optBytes := h[20:h.HeaderLen()]
	var opts []V4Option
	obl := len(optBytes)
	for i := 0; i < obl; {
		optionType := optBytes[i]
		switch optionType {
		case 0: // end of list
			break
		case 1: // byte padding
			opts = append(opts, V4Option(optBytes[i:i+1]))
			i++
		default:
			if i+1 < obl {
				optLen := int(optBytes[i+1])
				if i+optLen < obl {
					opts = append(opts, V4Option(optBytes[i:i+optLen]))
					i += optLen
					continue
				}
			}
			return nil, errors.New("option data is outside IPv4 header")
		}
	}
	return opts, nil
}

func (h V4Header) Packet() []byte {
	return h[:h.TotalLen()]
}

func (h V4Header) Payload() []byte {
	return h[h.HeaderLen():h.TotalLen()]
}

func (h V4Header) SetChecksum() {
	s := 0
	t := h.HeaderLen()
	for i := 0; i < t; i += 2 {
		s += int(h[i])<<8 | int(h[i+1])
	}
	for s > 0xffff {
		s = (s >> 16) + (s & 0xffff)
	}
	c := ^uint16(s)
	if c == 0 {
		// From RFC 768: If the computed checksum is zero, it is transmitted as all ones.
		c = 0xffff
	}
	binary.BigEndian.PutUint16(h[10:], c)
}

func (h V4Header) PseudoHeader(l4Proto int) []byte {
	b := make([]byte, 4*2+4)
	copy(b, h[12:20]) // source and destination
	b[9] = uint8(l4Proto)
	binary.BigEndian.PutUint16(b[10:], uint16(h.TotalLen()-h.HeaderLen()))
	return b
}

func (h V4Header) ProcessFragment(data *buf.Buffer, fragsMap map[uint16][]*buf.Buffer) *buf.Buffer {
	if h.Flags()&ipv4.MoreFragments == 0 && h.FragmentOffset() == 0 {
		return data
	}

	exist, ok := fragsMap[h.ID()]
	if !ok {
		// first fragment
		fragsMap[h.ID()] = []*buf.Buffer{data}
		return nil
	}

	last := V4Header(exist[len(exist)-1].Buf())
	exist = append(exist, data)
	if h.FragmentOffset() < last.FragmentOffset() {
		// Fragments didn't arrive in order. Sort them
		sort.Slice(exist, func(i, j int) bool {
			return V4Header(exist[i].Buf()).FragmentOffset() < V4Header(exist[j].Buf()).FragmentOffset()
		})
	} else {
		last = h
	}

	if last.Flags()&ipv4.MoreFragments != 0 {
		// last fragment hasn't arrived yet.
		return nil
	}

	// Ensure that there are no holes in the fragment chain
	lastPayload := 0
	expectedOffset := 0
	for _, data := range exist {
		eh := V4Header(data.Buf())
		if eh.FragmentOffset()*8 != expectedOffset {
			// There's a gap. Await more fragments
			return nil
		}
		lastPayload = eh.TotalLen() - eh.HeaderLen()
		expectedOffset += lastPayload
	}
	totalPayload := expectedOffset + lastPayload
	firstHeader := V4Header(exist[0].Buf())

	final := buf.DataPool.GetBuffer(firstHeader.HeaderLen() + totalPayload)
	fb := final.Buf()
	copy(fb[:firstHeader.HeaderLen()], firstHeader)
	offset := firstHeader.HeaderLen()
	for _, data := range exist {
		eh := V4Header(data.Buf())
		copy(fb[offset+eh.FragmentOffset()*8:], eh.Payload())
		buf.DataPool.PutBuffer(data)
	}
	firstHeader = fb
	firstHeader.SetFlags(firstHeader.Flags() &^ ipv4.MoreFragments)
	firstHeader.SetTotalLen(firstHeader.HeaderLen() + totalPayload)
	firstHeader.SetChecksum()
	return final
}

type V6Header []byte

func (h V6Header) Initialize() {
	for i := len(h) - 1; i > 0; i-- {
		h[i] = 0
	}
	h[0] = ipv6.Version << 4
}

func (h V6Header) Version() int {
	return int(h[0] >> 4)
}

func (h V6Header) TrafficClass() int {
	return int(h[0]&0x0f)<<4 | int(h[1])>>4
}

func (h V6Header) FlowLabel() int {
	return int(h[1]&0x0f)<<16 | int(h[2])<<8 | int(h[3])
}

func (h V6Header) PayloadLen() int {
	return int(binary.BigEndian.Uint16(h[4:6]))
}

func (h V6Header) NextHeader() int {
	return int(h[6])
}

func (h V6Header) HopLimit() int {
	return int(h[7])
}

func (h V6Header) SetTTL(hops int) {
	h[7] = uint8(hops)
}

func (h V6Header) Source() net.IP {
	return net.IP(h[8:24])
}

func (h V6Header) Destination() net.IP {
	return net.IP(h[24:40])
}

func (h V6Header) SetSource(ip net.IP) {
	if ip6 := ip.To16(); ip6 != nil {
		copy(h[8:24], ip)
	}
}

func (h V6Header) SetDestination(ip net.IP) {
	if ip6 := ip.To16(); ip6 != nil {
		copy(h[24:40], ip)
	}
}

func (h V6Header) HeaderLen() int {
	return ipv6.HeaderLen
}

func (h V6Header) TotalLen() int {
	return ipv6.HeaderLen + h.PayloadLen()
}

func (h V6Header) SetTotalLen(tl int) {
	binary.BigEndian.PutUint16(h[4:], uint16(tl)-ipv6.HeaderLen)
}

func (h V6Header) SetL4Protocol(proto int) {
	h[6] = uint8(proto)
}

func (h V6Header) L4Protocol() int {
	return h.NextHeader()
}

func (h V6Header) SetChecksum() {}

func (h V6Header) Packet() []byte {
	return h[:h.TotalLen()]
}

func (h V6Header) Payload() []byte {
	return h[ipv6.HeaderLen:h.TotalLen()]
}

func (h V6Header) PseudoHeader(l4Proto int) []byte {
	b := make([]byte, 16*2+8)
	copy(b, h[8:40]) // src and dst
	binary.BigEndian.PutUint32(b[32:], uint32(h.TotalLen()-h.HeaderLen()))
	b[39] = uint8(l4Proto)
	return b
}

func (h V6Header) ProcessFragments(data *buf.Buffer, fragsMap map[uint16][]*buf.Buffer) *buf.Buffer {
	// TODO: Implement based on Extension headers
	return nil
}

func ParseHeader(b []byte) (Header, error) {
	if len(b) == 0 {
		return nil, errors.New("empty header")
	}
	version := int(b[0] >> 4)
	switch version {
	case ipv4.Version:
		if len(b) < ipv4.HeaderLen {
			return nil, errors.New("ipv4 header too short")
		}
		return V4Header(b), nil

	case ipv6.Version:
		if len(b) < ipv6.HeaderLen {
			return nil, errors.New("ipv6 header too short")
		}
		return V6Header(b), nil
	default:
		return nil, fmt.Errorf("unhandled protocol version %d", version)
	}
}

func L4Checksum(ipHdr Header, checksumPosition, l4Proto int) {
	// reset current checksum, if any
	p := ipHdr.Payload()
	binary.BigEndian.PutUint16(p[checksumPosition:], 0)

	s := 0
	pl := ipHdr.TotalLen() - ipHdr.HeaderLen()
	if (pl % 2) != 0 {
		// uneven length, add last byte << 8
		pl--
		s = int(p[pl]) << 8
	}

	h := ipHdr.PseudoHeader(l4Proto)
	hl := len(h)
	for i := 0; i < hl; i += 2 {
		s += int(h[i])<<8 | int(h[i+1])
	}
	for i := 0; i < pl; i += 2 {
		s += int(p[i])<<8 | int(p[i+1])
	}
	for s > 0xffff {
		s = (s >> 16) + (s & 0xffff)
	}
	c := ^uint16(s)

	if c == 0 {
		// From RFC 768: If the computed checksum is zero, it is transmitted as all ones.
		c = 0xffff
	}
	// compute and assign checksum
	binary.BigEndian.PutUint16(p[checksumPosition:], c)
}

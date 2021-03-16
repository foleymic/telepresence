package tcp

import (
	"bytes"
	"fmt"
	"net"
	"sync/atomic"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/buf"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"
)

type Packet struct {
	IPHeader ip.Header
	MTUBuf   *buf.Buffer
}

var ipID uint32

func NextID() int {
	return int(atomic.AddUint32(&ipID, 1) & 0x0000ffff)
}

func NewIPPacket(ipPayloadLen int, src, dst net.IP) *Packet {
	pkg := Packet{}
	if len(src) == 4 && len(dst) == 4 {
		pkg.MTUBuf = buf.DataPool.GetBuffer(ipPayloadLen + ipv4.HeaderLen)
		iph := ip.V4Header(pkg.MTUBuf.Buf())
		iph.Initialize()
		iph.SetID(NextID())
		pkg.IPHeader = iph
	} else {
		pkg.MTUBuf = buf.DataPool.GetBuffer(ipPayloadLen + ipv6.HeaderLen)
		iph := ip.V6Header(pkg.MTUBuf.Buf())
		iph.Initialize()
		pkg.IPHeader = iph
	}
	iph := pkg.IPHeader
	iph.SetSource(src)
	iph.SetDestination(dst)
	iph.SetTTL(64)
	iph.SetTotalLen(iph.HeaderLen() + ipPayloadLen)
	return &pkg
}

func (p *Packet) Header() Header {
	return p.IPHeader.Payload()
}

func (p *Packet) String() string {
	b := bytes.Buffer{}
	ipHdr := p.IPHeader
	tcpHdr := p.Header()
	fmt.Fprintf(&b, "tcp sq %.3d, an %.3d, %s.%d -> %s.%d, flags=",
		tcpHdr.Sequence(), tcpHdr.AckNumber(), ipHdr.Source(), tcpHdr.SourcePort(), ipHdr.Destination(), tcpHdr.DestinationPort())
	tcpHdr.AppendFlags(&b)
	pl := tcpHdr.Payload()
	if len(pl) > 0 {
		b.WriteString(", payload: ")
		b.Write(pl)
	}
	return b.String()
}

func (p *Packet) Reset() *Packet {
	incIp := p.IPHeader
	incTcp := p.Header()

	pkt := NewIPPacket(HeaderLen, incIp.Source(), incIp.Destination())
	iph := pkt.IPHeader
	iph.SetL4Protocol(unix.IPPROTO_TCP)
	iph.SetChecksum()

	tcpHdr := Header(iph.Payload())
	tcpHdr.SetDataOffset(5)
	tcpHdr.SetSourcePort(incTcp.SourcePort())
	tcpHdr.SetDestinationPort(incTcp.DestinationPort())
	tcpHdr.SetWindowSize(uint16(maxReceiveWindow))
	tcpHdr.SetRST(true)
	tcpHdr.SetACK(true)

	// RFC 793:
	// "If the incoming segment has an ACK field, the reset takes its sequence
	// number from the ACK field of the segment, otherwise the reset has
	// sequence number zero and the ACK field is set to the sum of the sequence
	// number and segment length of the incoming segment. The connection remains
	// in the CLOSED state."
	if incTcp.ACK() {
		tcpHdr.SetSequence(incTcp.AckNumber())
		tcpHdr.SetAckNumber(incTcp.Sequence() + 1)
	} else {
		tcpHdr.SetSequence(0)
		tcpHdr.SetAckNumber(incTcp.Sequence() + uint32(len(incTcp.Payload())))
	}

	tcpHdr.SetChecksum(iph)
	return pkt
}

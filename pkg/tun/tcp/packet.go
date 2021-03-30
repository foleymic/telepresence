package tcp

import (
	"bytes"
	"fmt"
	"net"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/buffer"

	"golang.org/x/sys/unix"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"
)

type Packet struct {
	ip.Packet
}

func MakePacket(ipHdr ip.Header, data *buffer.Data) *Packet {
	return &Packet{Packet: ip.MakePacket(ipHdr, data)}
}

func NewPacket(ipPayloadLen int, src, dst net.IP) *Packet {
	return &Packet{Packet: ip.NewPacket(ipPayloadLen, src, dst)}
}

func (p *Packet) Header() Header {
	return p.IPHeader().Payload()
}

func (p *Packet) PayloadLen() int {
	return p.IPHeader().PayloadLen() - p.Header().DataOffset()*4
}

func (p *Packet) String() string {
	b := bytes.Buffer{}
	ipHdr := p.IPHeader()
	tcpHdr := p.Header()
	fmt.Fprintf(&b, "tcp sq %.3d, an %.3d, %s.%d -> %s.%d, flags=",
		tcpHdr.Sequence(), tcpHdr.AckNumber(), ipHdr.Source(), tcpHdr.SourcePort(), ipHdr.Destination(), tcpHdr.DestinationPort())
	tcpHdr.AppendFlags(&b)
	return b.String()
}

// Reset creates an ACK+RST packet for this packet.
func (p *Packet) Reset() *Packet {
	incIp := p.IPHeader()
	incTcp := p.Header()

	pkt := Packet{Packet: ip.NewPacket(HeaderLen, incIp.Source(), incIp.Destination())}
	iph := pkt.IPHeader()
	iph.SetL4Protocol(unix.IPPROTO_TCP)
	iph.SetChecksum()

	tcpHdr := Header(iph.Payload())
	tcpHdr.SetDataOffset(5)
	tcpHdr.SetSourcePort(incTcp.SourcePort())
	tcpHdr.SetDestinationPort(incTcp.DestinationPort())
	tcpHdr.SetWindowSize(uint16(maxReceiveWindow))
	tcpHdr.SetRST(true)
	tcpHdr.SetACK(true)

	if incTcp.ACK() {
		tcpHdr.SetSequence(incTcp.AckNumber())
		tcpHdr.SetAckNumber(incTcp.Sequence() + 1)
	} else {
		tcpHdr.SetSequence(0)
		tcpHdr.SetAckNumber(incTcp.Sequence() + uint32(len(incTcp.Payload())))
	}

	tcpHdr.SetChecksum(iph)
	return &pkt
}

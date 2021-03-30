package ip

import (
	"net"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/buffer"
)

type Packet struct {
	ipHdr Header
	data  *buffer.Data
}

func MakePacket(ipHdr Header, data *buffer.Data) Packet {
	return Packet{ipHdr: ipHdr, data: data}
}

func NewPacket(ipPayloadLen int, src, dst net.IP) Packet {
	pkg := Packet{}
	if len(src) == 4 && len(dst) == 4 {
		pkg.data = buffer.DataPool.Get(ipPayloadLen + ipv4.HeaderLen)
		iph := V4Header(pkg.data.Buf())
		iph.Initialize()
		iph.SetID(NextID())
		pkg.ipHdr = iph
	} else {
		pkg.data = buffer.DataPool.Get(ipPayloadLen + ipv6.HeaderLen)
		iph := V6Header(pkg.data.Buf())
		iph.Initialize()
		pkg.ipHdr = iph
	}
	iph := pkg.ipHdr
	iph.SetSource(src)
	iph.SetDestination(dst)
	iph.SetTTL(64)
	iph.SetPayloadLen(ipPayloadLen)
	return pkg
}

func (p *Packet) IPHeader() Header {
	return p.ipHdr
}

func (p *Packet) Data() *buffer.Data {
	return p.data
}

func (p *Packet) Release() {
	buffer.DataPool.Put(p.data)
}

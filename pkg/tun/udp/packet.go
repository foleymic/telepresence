package udp

import (
	"github.com/telepresenceio/telepresence/v2/pkg/tun/buffer"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"
)

type Datagram struct {
	ip.Packet
}

func MakePacket(ipHdr ip.Header, data *buffer.Data) *Datagram {
	return &Datagram{Packet: ip.MakePacket(ipHdr, data)}
}

func (p *Datagram) Header() Header {
	return p.IPHeader().Payload()
}

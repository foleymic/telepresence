package udp

import (
	"net"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"
	"golang.org/x/net/proxy"
)

type Handler struct {
	id          ip.ConnID
	remove      func()
	socksDialer proxy.ContextDialer
	toTun       chan<- interface{}
	fromTun     chan *Datagram
	toSocks     chan *Datagram
	socksConn   net.Conn
}

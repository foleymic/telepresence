package tun

import (
	"context"
	"fmt"
	"sync"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/buf"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/tcp"

	ip2 "github.com/telepresenceio/telepresence/v2/pkg/tun/ip"

	"golang.org/x/net/proxy"

	"golang.org/x/sys/unix"

	"golang.org/x/net/ipv4"

	"golang.org/x/net/ipv6"

	"github.com/datawire/dlib/dlog"

	"github.com/datawire/dlib/dgroup"
)

type Dispatcher struct {
	dev         *Device
	socksDialer proxy.Dialer
	udpHandlers map[ip2.ConnID]*udpHandler
	tcpHandlers map[ip2.ConnID]*tcp.ConnTrack
	handlersWg  sync.WaitGroup
	toTunCh     chan interface{}
	lock        sync.Mutex
}

func newDispatcher(dev *Device, socksDialer proxy.Dialer) *Dispatcher {
	return &Dispatcher{
		dev:         dev,
		socksDialer: socksDialer,
		udpHandlers: make(map[ip2.ConnID]*udpHandler),
		tcpHandlers: make(map[ip2.ConnID]*tcp.ConnTrack),
		toTunCh:     make(chan interface{}),
	}
}

func (d *Dispatcher) deleteHandler(id ip2.ConnID) {
	d.lock.Lock()
	delete(d.udpHandlers, id)
	d.lock.Unlock()
}

func newSocksDialer(port int) (proxy.Dialer, error) {
	return proxy.SOCKS5("tcp", fmt.Sprintf("localhost:%d", port), nil, proxy.Direct)
}

func (d *Dispatcher) Stop(c context.Context) {
	for _, handler := range d.tcpHandlers {
		handler.Close(c)
	}
	d.handlersWg.Wait()
}

func (d *Dispatcher) Run(c context.Context) error {
	g := dgroup.NewGroup(c, dgroup.GroupConfig{})
	// writer
	g.Go("writer", func(c context.Context) error {
		for {
			select {
			case <-c.Done():
				dlog.Info(c, "quit")
				return nil
			case pkt := <-d.toTunCh:
				switch pkt := pkt.(type) {
				case *tcp.Packet:
					dlog.Debugf(c, "write to TUN: %s", pkt)
					_, err := d.dev.Write(pkt.MTUBuf)
					buf.DataPool.PutBuffer(pkt.MTUBuf)
					if err != nil {
						if c.Err() != nil {
							err = nil
						}
						return err
					}

					/*
						case *udpPacket:
							udp := pkt.(*udpPacket)
							d.dev.Write(udp.wire)
							releaseUDPPacket(udp)
						case *ipPacket:
							ip := pkt.(*ipPacket)
							d.dev.Write(ip.wire)
							releaseIPPacket(ip)
					*/
				}
			}
		}
	})

	g.Go("reader", func(c context.Context) error {
		fragmentMap := make(map[uint16][]*buf.Buffer)
		for {
			data := buf.DataPool.GetBuffer(buf.Size)
			n, err := d.dev.Read(data)
			if err != nil {
				return fmt.Errorf("read packet error: %v", err)
			}
			if n == 0 {
				continue
			}
			data.SetLength(n)
			hdr, err := ip.ParseHeader(data.Buf())
			if err != nil {
				dlog.Error(c, "Unable to parse package header")
				buf.DataPool.PutBuffer(data)
				continue
			}

			if hdr.Version() == ipv6.Version {
				dlog.Error(c, "IPv6 is not yet handled by this dispatcher")
				buf.DataPool.PutBuffer(data)
				continue
			}

			ipHdr := hdr.(ip2.V4Header)
			if ipHdr.Flags()&ipv4.MoreFragments != 0 || ipHdr.FragmentOffset() != 0 {
				data = ipHdr.ProcessFragment(data, fragmentMap)
				if data == nil {
					continue
				}
				ipHdr = data.Buf()
			}

			switch ipHdr.L4Protocol() {
			case unix.IPPROTO_TCP:
				// data is handed over to dispatcher.
				d.tcp(c, &tcp.Packet{IPHeader: ipHdr, MTUBuf: data})
			case unix.IPPROTO_UDP:
				dlog.Debugf(c, "discarding UDP package to %s:%d", ipHdr.Destination(), udpDatagram(ipHdr.Payload()).destination())
				buf.DataPool.PutBuffer(data)
			default:
				buf.DataPool.PutBuffer(data)
			}
		}
	})
	return g.Wait()
}

func (d *Dispatcher) createTCPConnTrack(c context.Context, id ip2.ConnID) *tcp.ConnTrack {
	track := tcp.NewConnTrack(d.socksDialer.(proxy.ContextDialer), d.toTunCh, id, func() {
		d.clearTCPConnTrack(c, id)
	})
	d.lock.Lock()
	d.tcpHandlers[id] = track
	count := len(d.tcpHandlers)
	d.lock.Unlock()
	d.handlersWg.Add(1)
	go track.Run(c, &d.handlersWg)
	dlog.Debugf(c, "tracking %d TCP connections", count)
	return track
}

func (d *Dispatcher) getTCPConnTrack(id ip2.ConnID) *tcp.ConnTrack {
	d.lock.Lock()
	handler := d.tcpHandlers[id]
	d.lock.Unlock()
	return handler
}

func (d *Dispatcher) clearTCPConnTrack(c context.Context, id ip2.ConnID) {
	d.lock.Lock()
	delete(d.tcpHandlers, id)
	count := len(d.tcpHandlers)
	d.lock.Unlock()
	dlog.Debugf(c, "tracking %d TCP connections", count)
}

func (d *Dispatcher) tcp(c context.Context, pkt *tcp.Packet) {
	ipHdr := pkt.IPHeader
	tcpHdr := pkt.Header()
	connID := ip2.NewConnID(ipHdr, tcpHdr.SourcePort(), tcpHdr.DestinationPort())
	track := d.getTCPConnTrack(connID)
	if track == nil {
		// ignore RST, if there is no track of this connection
		if tcpHdr.RST() {
			dlog.Debug(c, "dispatching got RST without but connection is not yet tracked")
			return
		}

		// return a RST to non-SYN packet
		if !tcpHdr.SYN() {
			select {
			case <-c.Done():
				return
			case d.toTunCh <- pkt.Reset():
			}
		}
		track = d.createTCPConnTrack(c, connID)
	}
	track.NewPacket(c, pkt)
}

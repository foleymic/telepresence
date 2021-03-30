package tun

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/udp"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/net/proxy"
	"golang.org/x/sys/unix"

	"github.com/datawire/dlib/dgroup"
	"github.com/datawire/dlib/dlog"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/buffer"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/tcp"
)

type PacketWithAckFunc struct {
	Packet  interface{}
	AckFunc func(context.Context)
}

type Dispatcher struct {
	dev            *Device
	socksTCPDialer proxy.Dialer
	socksUDPDialer proxy.Dialer
	udpHandlers    map[ip.ConnID]*udp.Handler
	tcpHandlers    map[ip.ConnID]*tcp.Workflow
	handlersWg     sync.WaitGroup
	toTunCh        chan *PacketWithAckFunc
	closed         int32
	lock           sync.Mutex
}

func NewDispatcher(dev *Device, socksTCPDialer, socksUDPDialer proxy.Dialer) *Dispatcher {
	return &Dispatcher{
		dev:            dev,
		socksTCPDialer: socksTCPDialer,
		socksUDPDialer: socksUDPDialer,
		udpHandlers:    make(map[ip.ConnID]*udp.Handler),
		tcpHandlers:    make(map[ip.ConnID]*tcp.Workflow),
		toTunCh:        make(chan *PacketWithAckFunc, 100}),
	}
}

func (d *Dispatcher) deleteHandler(id ip.ConnID) {
	d.lock.Lock()
	delete(d.udpHandlers, id)
	d.lock.Unlock()
}

func NewSocksDialer(proto string, port int) (proxy.Dialer, error) {
	return proxy.SOCKS5(proto, fmt.Sprintf("127.0.0.1:%d", port), nil, proxy.Direct)
}

func (d *Dispatcher) Stop(c context.Context) {
	for _, handler := range d.tcpHandlers {
		handler.Close(c)
	}
	d.handlersWg.Wait()
	atomic.StoreInt32(&d.closed, 1)
	d.dev.Close()
}

func (d *Dispatcher) Run(c context.Context) error {
	g := dgroup.NewGroup(c, dgroup.GroupConfig{})
	// writer
	g.Go("writer", func(c context.Context) error {
		for {
			select {
			case <-c.Done():
				return nil
			case pwa := <-d.toTunCh:
				if atomic.LoadInt32(&d.closed) != 0 {
					continue
				}
				pkt := pwa.Packet
				switch pkt := pkt.(type) {
				case *tcp.Packet:
					dlog.Debugf(c, "-> TUN: %s", pkt)
					_, err := d.dev.Write(pkt.Data())
					if err == nil {
						pwa.AckFunc(c)
					}
					pkt.Release()
					if err != nil {
						if atomic.LoadInt32(&d.closed) != 0 {
							continue
						}
						if c.Err() != nil {
							err = nil
						}
						return err
					}
				}
			}
		}
	})

	g.Go("reader", func(c context.Context) error {
		fragmentMap := make(map[uint16][]*buffer.Data)
		for atomic.LoadInt32(&d.closed) == 0 {
			data := buffer.DataPool.Get(buffer.DataPool.MTU)
			n, err := d.dev.Read(data)
			if err != nil {
				if c.Err() != nil || atomic.LoadInt32(&d.closed) != 0 {
					return nil
				}
				return fmt.Errorf("read packet error: %v", err)
			}
			if n == 0 {
				continue
			}
			data.SetLength(n)
			hdr, err := ip.ParseHeader(data.Buf())
			if err != nil {
				dlog.Error(c, "Unable to parse package header")
				buffer.DataPool.Put(data)
				continue
			}

			if hdr.Version() == ipv6.Version {
				dlog.Error(c, "IPv6 is not yet handled by this dispatcher")
				buffer.DataPool.Put(data)
				continue
			}

			ipHdr := hdr.(ip.V4Header)
			if ipHdr.Flags()&ipv4.MoreFragments != 0 || ipHdr.FragmentOffset() != 0 {
				data = ipHdr.ConcatFragments(data, fragmentMap)
				if data == nil {
					continue
				}
				ipHdr = data.Buf()
			}

			switch ipHdr.L4Protocol() {
			case unix.IPPROTO_TCP:
				// data is handed over to dispatcher.
				d.tcp(c, tcp.MakePacket(ipHdr, data))
			case unix.IPPROTO_UDP:
				dlog.Debugf(c, "discarding UDP package to %s:%d", ipHdr.Destination(), udp.Header(ipHdr.Payload()).DestinationPort())
				buffer.DataPool.Put(data)
			default:
				buffer.DataPool.Put(data)
			}
		}
		return nil
	})
	return g.Wait()
}

func (d *Dispatcher) connIDs() []string {
	d.lock.Lock()
	ids := make([]string, len(d.tcpHandlers))
	i := 0
	for id := range d.tcpHandlers {
		ids[i] = id.String()
		i++
	}
	d.lock.Unlock()
	sort.Strings(ids)
	return ids
}

func (d *Dispatcher) createTCPWorkflow(c context.Context, id ip.ConnID) *tcp.Workflow {
	wf := tcp.NewWorkflow(d.socksTCPDialer.(proxy.ContextDialer), d.toTunCh, id, func() {
		d.clearTCPWorkflow(c, id)
	})
	d.lock.Lock()
	d.tcpHandlers[id] = wf
	d.lock.Unlock()
	d.handlersWg.Add(1)
	go wf.Run(c, &d.handlersWg)
	dlog.Debugf(c, "tracking TCP connections %s", strings.Join(d.connIDs(), ", "))
	return wf
}

func (d *Dispatcher) getTCPWorkflow(id ip.ConnID) *tcp.Workflow {
	d.lock.Lock()
	handler := d.tcpHandlers[id]
	d.lock.Unlock()
	return handler
}

func (d *Dispatcher) clearTCPWorkflow(c context.Context, id ip.ConnID) {
	d.lock.Lock()
	delete(d.tcpHandlers, id)
	d.lock.Unlock()
	dlog.Debugf(c, "tracking TCP connections %s", strings.Join(d.connIDs(), ", "))
}

func (d *Dispatcher) tcp(c context.Context, pkt *tcp.Packet) {
	ipHdr := pkt.IPHeader()
	tcpHdr := pkt.Header()
	connID := ip.NewConnID(ipHdr, tcpHdr.SourcePort(), tcpHdr.DestinationPort())
	wf := d.getTCPWorkflow(connID)
	if wf == nil {
		if tcpHdr.RST() {
			dlog.Debug(c, "dispatching got RST without connection workflow")
			return
		}
		if !tcpHdr.SYN() {
			select {
			case <-c.Done():
				return
			case d.toTunCh <- pkt.Reset():
			}
		}
		wf = d.createTCPWorkflow(c, connID)
	}
	wf.NewPacket(c, pkt)
}

func (d *Dispatcher) AddSubnets(c context.Context, subnets []*net.IPNet) error {
	for _, sn := range subnets {
		to := make(net.IP, len(sn.IP))
		copy(to, sn.IP)
		to[len(to)-1] = 1
		if err := d.dev.AddSubnet(c, sn, to); err != nil {
			return err
		}
	}
	return nil
}

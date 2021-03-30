package daemon

import (
	"context"
	"net"

	"github.com/telepresenceio/telepresence/v2/pkg/subnet"

	"github.com/telepresenceio/telepresence/v2/pkg/client/daemon/nat"
	"github.com/telepresenceio/telepresence/v2/pkg/tun"
)

type tunRouter struct {
	dispatcher *tun.Dispatcher
	ips        map[string]net.IP
	subnets    map[string]*net.IPNet
}

func NewTunRouter() (nat.FirewallRouter, error) {
	td, err := tun.OpenTun()
	if err != nil {
		return nil, err
	}
	tcpDialer, err := tun.NewSocksDialer("tcp", 1080)
	if err != nil {
		return nil, err
	}
	udpDialer, err := tun.NewSocksDialer("udp", 1080)
	if err != nil {
		return nil, err
	}
	return &tunRouter{
		dispatcher: tun.NewDispatcher(td, tcpDialer, udpDialer),
		ips:        make(map[string]net.IP),
		subnets:    make(map[string]*net.IPNet),
	}, nil
}

func (t *tunRouter) Flush(c context.Context) error {
	addedNets := make(map[string]*net.IPNet)
	ips := make([]net.IP, len(t.ips))
	i := 0
	for _, ip := range t.ips {
		ips[i] = ip
		i++
	}
	for _, sn := range subnet.AnalyzeIPs(ips) {
		addedNets[sn.String()] = sn
	}

	droppedNets := make(map[string]*net.IPNet)
	for k, sn := range t.subnets {
		if _, ok := addedNets[k]; ok {
			delete(addedNets, k)
		} else {
			droppedNets[k] = sn
		}
	}
	if len(addedNets) > 0 {
		subnets := make([]*net.IPNet, len(addedNets))
		i = 0
		for k, sn := range addedNets {
			t.subnets[k] = sn
			subnets[i] = sn
			i++
		}
		return t.dispatcher.AddSubnets(c, subnets)
	}
	// TODO remove subnets that are no longer in use
	return nil
}

func (t *tunRouter) Clear(_ context.Context, route *nat.Route) (bool, error) {
	ip := route.IP()
	k := ip.String()
	if _, ok := t.ips[k]; ok {
		delete(t.ips, k)
		return true, nil
	}
	return false, nil
}

func (t *tunRouter) Add(_ context.Context, route *nat.Route) (bool, error) {
	ip := route.IP()
	k := ip.String()
	if _, ok := t.ips[k]; ok {
		return false, nil
	}
	t.ips[k] = ip
	return true, nil
}

func (t *tunRouter) Disable(c context.Context) error {
	t.dispatcher.Stop(c)
	return nil
}

func (t *tunRouter) Enable(c context.Context) error {
	go t.dispatcher.Run(c)
	return nil
}

func (t *tunRouter) GetOriginalDst(conn *net.TCPConn) (host string, err error) {
	panic("implement me")
}

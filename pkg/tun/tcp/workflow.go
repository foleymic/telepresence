package tcp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/net/proxy"
	"golang.org/x/sys/unix"

	"github.com/datawire/dlib/dlog"
	"github.com/telepresenceio/telepresence/v2/pkg/client"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/buffer"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"
)

type state int32

const (
	// simplified server-side tcp states
	stateClosed state = iota
	stateSynReceived
	stateEstablished
	stateFinWait1
	stateFinWait2
	stateClosing
	stateLastACK
)

const maxReceiveWindow int = 65535
const maxSendWindow int = 65535

type Workflow struct {
	id          ip.ConnID
	remove      func()
	socksDialer proxy.ContextDialer
	toTun       chan<- interface{}
	fromTun     chan *Packet
	toSocks     chan *Packet
	ackCh       chan struct{}
	closeCh     chan error

	socksConn net.Conn

	wfState    state
	nextSeq    uint32
	rcvNextSeq uint32

	sendWnd int32
	rcvWnd  int32

	lstAck uint32

	windowCond *sync.Cond
}

func (c *Workflow) state() state {
	return state(atomic.LoadInt32((*int32)(&c.wfState)))
}

func (c *Workflow) setState(s state) {
	atomic.StoreInt32((*int32)(&c.wfState), int32(s))
}

func (c *Workflow) nextSequence() uint32 {
	return atomic.LoadUint32(&c.nextSeq)
}

func (c *Workflow) addNextSequence(v uint32) {
	atomic.AddUint32(&c.nextSeq, v)
}

func (c *Workflow) setNextSequence(v uint32) {
	atomic.StoreUint32(&c.nextSeq, v)
}

func (c *Workflow) receiveNextSequence() uint32 {
	return atomic.LoadUint32(&c.rcvNextSeq)
}

func (c *Workflow) addReceiveNextSequence(v uint32) {
	atomic.AddUint32(&c.rcvNextSeq, v)
}

func (c *Workflow) setReceiveNextSequence(v uint32) {
	atomic.StoreUint32(&c.rcvNextSeq, v)
}

func (c *Workflow) lastAck() uint32 {
	return atomic.LoadUint32(&c.lstAck)
}

func (c *Workflow) setLastAck(v uint32) {
	atomic.StoreUint32(&c.lstAck, v)
}

func (c *Workflow) sendWindow() int32 {
	return atomic.LoadInt32(&c.sendWnd)
}

func (c *Workflow) setSendWindow(v int32) {
	atomic.StoreInt32(&c.sendWnd, v)
}

func (c *Workflow) receiveWindow() int32 {
	return atomic.LoadInt32(&c.rcvWnd)
}

func (c *Workflow) setReceiveWindow(v int32) {
	atomic.StoreInt32(&c.rcvWnd, v)
}

func NewWorkflow(socksDialer proxy.ContextDialer, toTunCh chan<- interface{}, id ip.ConnID, remove func()) *Workflow {
	return &Workflow{
		id:          id,
		remove:      remove,
		socksDialer: socksDialer,
		toTun:       toTunCh,
		fromTun:     make(chan *Packet, 0x400),
		toSocks:     make(chan *Packet, 0x400),
		ackCh:       make(chan struct{}, 0x40),
		closeCh:     make(chan error, 5),

		sendWnd:    int32(maxSendWindow),
		rcvWnd:     int32(maxReceiveWindow),
		windowCond: &sync.Cond{L: &sync.Mutex{}},
		wfState:    stateClosed,
	}
}

func (c *Workflow) NewPacket(ctx context.Context, pkt *Packet) {
	select {
	case <-ctx.Done():
		c.closeCh <- errQuitByContext
	case c.fromTun <- pkt:
	}
}

func (c *Workflow) Close(_ context.Context) {
	c.closeCh <- errQuitByOther
}

func (c *Workflow) validAck(tcpHdr Header) bool {
	return tcpHdr.AckNumber() == c.nextSequence()
}

func (c *Workflow) validSeq(tcpHdr Header) bool {
	return tcpHdr.Sequence() == c.receiveNextSequence()
}

func (c *Workflow) sendToSocks(ctx context.Context, pkt *Packet) bool {
	payloadLen := uint32(len(pkt.Header().Payload()))
	select {
	case c.toSocks <- pkt:
		c.addReceiveNextSequence(payloadLen)
		c.ackCh <- struct{}{}

		// reduce window when recved
		wnd := c.receiveWindow()
		wnd -= int32(payloadLen)
		if wnd < 0 {
			wnd = 0
		}
		c.setReceiveWindow(wnd)

		return true
	case <-ctx.Done():
		c.closeCh <- errQuitByContext
		return false
	}
}

func (c *Workflow) sendToTun(ctx context.Context, pkt *Packet) {
	tcpHdr := pkt.Header()
	if tcpHdr.ACK() {
		c.setLastAck(tcpHdr.AckNumber())
	}
	select {
	case <-ctx.Done():
		c.closeCh <- errQuitByContext
	case c.toTun <- pkt:
	}
}

func (c *Workflow) newPacket(ipPlayloadLen int) *Packet {
	pkg := NewIPPacket(ipPlayloadLen, c.id.Destination(), c.id.Source())
	ipHdr := pkg.ipHdr
	ipHdr.SetL4Protocol(unix.IPPROTO_TCP)
	ipHdr.SetChecksum()

	tcpHdr := Header(ipHdr.Payload())
	tcpHdr.SetDataOffset(5)
	tcpHdr.SetSourcePort(c.id.DestinationPort())
	tcpHdr.SetDestinationPort(c.id.SourcePort())
	tcpHdr.SetWindowSize(uint16(c.receiveWindow()))
	return pkg
}

func (c *Workflow) synAck(ctx context.Context, syn *Packet) {
	syntcpHdr := syn.Header()
	if !syntcpHdr.SYN() {
		return
	}
	hl := HeaderLen
	if syntcpHdr.ECE() {
		hl += 4
	}
	pkg := c.newPacket(hl)
	tcpHdr := pkg.Header()
	tcpHdr.SetSYN(true)
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(c.nextSequence())
	tcpHdr.SetAckNumber(syntcpHdr.Sequence() + 1)
	if syntcpHdr.ECE() {
		tcpHdr.SetDataOffset(6)
		opts := tcpHdr.OptionBytes()
		opts[0] = 2
		opts[1] = 4
		binary.BigEndian.PutUint16(opts[2:], buffer.MTU-uint16(pkg.ipHdr.HeaderLen()+HeaderLen))
	}
	tcpHdr.SetChecksum(pkg.ipHdr)
	c.sendToTun(ctx, pkg)

	// SYN counts 1 seq
	c.addNextSequence(1)
}

func (c *Workflow) finAck(ctx context.Context) {
	pkg := c.newPacket(HeaderLen)
	tcpHdr := pkg.Header()
	tcpHdr.SetFIN(true)
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(c.nextSequence())
	tcpHdr.SetAckNumber(c.receiveNextSequence())
	tcpHdr.SetChecksum(pkg.ipHdr)
	c.sendToTun(ctx, pkg)
	// FIN counts 1 seq
	c.addNextSequence(1)
}

func (c *Workflow) ack(ctx context.Context) {
	ackNum := c.receiveNextSequence()
	if c.receiveNextSequence() == c.lastAck() {
		return
	}
	pkg := c.newPacket(HeaderLen)
	tcpHdr := pkg.Header()
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(c.nextSequence())
	tcpHdr.SetAckNumber(ackNum)
	tcpHdr.SetChecksum(pkg.ipHdr)
	c.sendToTun(ctx, pkg)
}

func (c *Workflow) socksWriterLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			c.closeCh <- errQuitByContext
			return
		case pkt := <-c.toSocks:
			tcpHdr := pkt.Header()
			dlog.Debugf(ctx, "socksConn Write: %s", pkt)
			data := tcpHdr.Payload()
			for len(data) > 0 {
				n, err := c.socksConn.Write(data)
				if err != nil {
					if ctx.Err() == nil {
						dlog.Error(ctx, err)
					}
					return
				}
				data = data[n:]
			}

			// increase window when processed
			wnd := c.receiveWindow()
			wnd += int32(len(tcpHdr.Payload()))
			if wnd > int32(maxReceiveWindow) {
				wnd = int32(maxReceiveWindow)
			}
			c.setReceiveWindow(wnd)
			buffer.DataPool.PutBuffer(pkt.data)
		}
	}
}

func (c *Workflow) socksReaderLoop(ctx context.Context) {
	var bothHeadersLen int
	if c.id.IsIPv4() {
		bothHeadersLen = ipv4.HeaderLen + HeaderLen
	} else {
		bothHeadersLen = ipv6.HeaderLen + HeaderLen
	}

	for {
		window := c.sendWindow()
		if window <= 0 {
			for window <= 0 {
				c.windowCond.L.Lock()
				c.windowCond.Wait()
				window = c.sendWindow()
			}
			c.windowCond.L.Unlock()
		}

		maxRead := int(window)
		if maxRead > buffer.MTU-bothHeadersLen {
			maxRead = buffer.MTU - bothHeadersLen
		}

		pkt := c.newPacket(HeaderLen + maxRead)
		ipHdr := pkt.ipHdr
		tcpHdr := pkt.Header()
		n, err := c.socksConn.Read(tcpHdr.Payload())
		if err != nil {
			if ctx.Err() == nil && c.state() < stateFinWait1 {
				c.closeCh <- err
			}
			return
		}

		if n < maxRead {
			ipHdr.SetPayloadLen(n + HeaderLen)
			ipHdr.SetChecksum()
			pkt.data.SetLength(n + bothHeadersLen)
		}

		tcpHdr.SetACK(true)
		tcpHdr.SetPSH(true)
		tcpHdr.SetSequence(c.nextSequence())
		tcpHdr.SetAckNumber(c.receiveNextSequence())
		tcpHdr.SetChecksum(ipHdr)

		c.sendToTun(ctx, pkt)
		// adjust seq
		c.addNextSequence(uint32(len(tcpHdr.Payload())))

		nxt := window - int32(n)
		if nxt < 0 {
			nxt = 0
		}
		// if sendWindow does not equal to wnd, it is already updated by a
		// received pkt from TUN
		atomic.CompareAndSwapInt32(&c.sendWnd, window, nxt)
	}
}

var errQuitByOther = errors.New("quitByOther")
var errTimedWait = errors.New("quitBySelf")
var errQuitByReset = errors.New("quitByReset")
var errQuitByContext = errors.New("quitByContext")

func (c *Workflow) dialSocks(ctx context.Context) (net.Conn, error) {
	dlog.Debugf(ctx, "SOCKS5 DialContext %s", c.id)

	tos := &client.GetConfig(ctx).Timeouts
	ctx, cancel := context.WithTimeout(ctx, tos.ProxyDial)
	defer cancel()
	conn, err := c.socksDialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", c.id.Destination(), c.id.DestinationPort()))
	if err != nil {
		return nil, client.CheckTimeout(ctx, &tos.ProxyDial, fmt.Errorf("fail to connect SOCKS proxy: %v", err))
	}
	return conn, nil
}

// stateClosed receives a SYN packet, tries to connect the socks proxy, gives a
// SYN/ACK if success, otherwise RST
func (c *Workflow) closed(ctx context.Context, syn *Packet) error {
	// dlog.Debugf(ctx, "stateClosed %s.%d", c.id.Destination(), c.id.DestinationPort())
	for i := 0; i < 2; i++ {
		conn, err := c.dialSocks(ctx)
		if err != nil {
			dlog.Error(ctx, err)
			continue
		}
		c.socksConn = conn
		break
	}

	if c.socksConn == nil {
		select {
		case <-ctx.Done():
			return errQuitByContext
		case c.toTun <- syn.Reset():
		}
		return errQuitByReset
	}
	// context variables
	c.setReceiveNextSequence(syn.Header().Sequence() + 1)
	c.setNextSequence(1)

	c.synAck(ctx, syn)
	c.setState(stateSynReceived)
	return nil
}

func (c *Workflow) synReceived(ctx context.Context, pkt *Packet) (bool, error) {
	// dlog.Debugf(ctx, "stateSynReceived %s", c.id)
	tcpHdr := pkt.Header()
	if !(c.validSeq(tcpHdr) && c.validAck(tcpHdr)) {
		if !tcpHdr.RST() {
			select {
			case <-ctx.Done():
				return true, errQuitByContext
			case c.toTun <- pkt.Reset():
			}
		}
		return true, nil
	}
	if tcpHdr.RST() {
		return true, errQuitByReset
	}
	if !tcpHdr.ACK() {
		return true, nil
	}

	putBack := true
	c.setState(stateEstablished)
	go c.socksWriterLoop(ctx)
	go c.socksReaderLoop(ctx)

	if len(tcpHdr.Payload()) != 0 {
		if c.sendToSocks(ctx, pkt) {
			putBack = false
		}
	}
	return putBack, nil
}

func (c *Workflow) established(ctx context.Context, pkt *Packet) (bool, error) {
	// dlog.Debugf(ctx, "stateEstablished %s", c.id)
	tcpHdr := pkt.Header()
	if !c.validSeq(tcpHdr) {
		c.ack(ctx)
		return true, nil
	}
	if tcpHdr.RST() {
		return true, errQuitByReset
	}
	if !tcpHdr.ACK() {
		return true, nil
	}

	putBack := true
	if len(tcpHdr.Payload()) != 0 {
		if c.sendToSocks(ctx, pkt) {
			putBack = false
		}
	}
	if tcpHdr.FIN() {
		c.addReceiveNextSequence(1)
		c.finAck(ctx)
		c.setState(stateLastACK)
		return putBack, errQuitByOther
	}
	return putBack, nil
}

func (c *Workflow) finWait1(ctx context.Context, pkt *Packet) error {
	// dlog.Debugf(ctx, "stateFinWait1 %s", c.id)
	tcpHdr := pkt.Header()
	if !c.validSeq(tcpHdr) {
		return nil
	}
	if tcpHdr.RST() {
		return errQuitByReset
	}
	if !tcpHdr.ACK() {
		return nil
	}

	if tcpHdr.FIN() {
		c.addReceiveNextSequence(1)
		c.ack(ctx)
		if c.validAck(tcpHdr) {
			return errTimedWait
		} else {
			c.setState(stateClosing)
			return nil
		}
	} else {
		c.setState(stateFinWait2)
		return nil
	}
}

func (c *Workflow) finWait2(ctx context.Context, pkt *Packet) error {
	// dlog.Debugf(ctx, "stateFinWait2 %s", c.id)
	tcpHdr := pkt.Header()
	if !(c.validSeq(tcpHdr) && c.validAck(tcpHdr)) {
		return nil
	}
	if tcpHdr.RST() {
		return errQuitByReset
	}
	if !tcpHdr.ACK() {
		return nil
	}
	c.addReceiveNextSequence(1)
	c.ack(ctx)
	c.setState(stateClosed)
	return errTimedWait
}

func (c *Workflow) closing(_ context.Context, pkt *Packet) error {
	// dlog.Debugf(ctx, "stateClosing %s", c.id)
	tcpHdr := pkt.Header()
	if !(c.validSeq(tcpHdr) && c.validAck(tcpHdr)) {
		return nil
	}
	if tcpHdr.RST() {
		return errQuitByReset
	}
	if !tcpHdr.ACK() {
		return nil
	}
	return errTimedWait
}

func (c *Workflow) atLastAck(_ context.Context, pkt *Packet) error {
	// dlog.Debugf(ctx, "stateLastAck %s", c.id)
	tcpHdr := pkt.Header()
	if !(c.validSeq(tcpHdr) && c.validAck(tcpHdr)) {
		return nil
	}
	if !tcpHdr.ACK() {
		return nil
	}
	return errTimedWait
}

func (c *Workflow) updateSendWindow(pkt *Packet) {
	c.setSendWindow(int32(pkt.Header().WindowSize()))
	c.windowCond.Signal()
}

func (c *Workflow) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	go c.processAcks(ctx)
	for c.processFromTun(ctx) {
	}
}

func (c *Workflow) processFromTun(ctx context.Context) bool {
	select {
	case pkt := <-c.fromTun:
		c.updateSendWindow(pkt)

		dlog.Debugf(ctx, "read from TUN %s", pkt)

		var end error
		pubBack := true
		switch c.state() {
		case stateClosed:
			end = c.closed(ctx, pkt)
		case stateSynReceived:
			pubBack, end = c.synReceived(ctx, pkt)
		case stateEstablished:
			pubBack, end = c.established(ctx, pkt)
		case stateFinWait1:
			end = c.finWait1(ctx, pkt)
		case stateFinWait2:
			end = c.finWait2(ctx, pkt)
		case stateClosing:
			end = c.closing(ctx, pkt)
		case stateLastACK:
			end = c.atLastAck(ctx, pkt)
		}
		if pubBack {
			buffer.DataPool.PutBuffer(pkt.data)
		}
		if end != nil {
			c.closeCh <- end
			return true
		}

	case err := <-c.closeCh:
		switch err {
		case errQuitByOther:
			c.finAck(ctx)
			c.setState(stateFinWait1)
			return true
		case errTimedWait:
			c.setState(stateClosed)
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			return c.processFromTun(ctx)
		}
		c.remove()
		if c.socksConn != nil {
			c.socksConn.Close()
		}
		return false

	case <-ctx.Done():
		c.remove()
		if c.socksConn != nil {
			c.socksConn.Close()
		}
		return false
	}
	return true
}

func (c *Workflow) processAcks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.ackCh:
			c.ack(ctx)
		}
	}
}

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
	"github.com/datawire/dlib/dtime"
	"github.com/telepresenceio/telepresence/v2/pkg/client"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/buffer"
	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"
)

type state int32

const (
	// simplified server-side tcp states
	stateIdle state = iota
	stateSynReceived
	stateEstablished
	stateFinWait1
	stateFinWait2
	stateClosing
	stateLastACK
)

const maxReceiveWindow = 65535
const maxSendWindow = 65535
const ioChannelSize = 0x400

type Workflow struct {
	id          ip.ConnID
	remove      func()
	socksDialer proxy.ContextDialer
	toTun       chan<- interface{}
	fromTun     chan *Packet
	toSocks     chan *Packet
	closeCh     chan error

	socksConn net.Conn

	wfState    state
	nextSeq    uint32
	rcvNextSeq uint32

	sendWnd int32
	rcvWnd  int32

	lstAck uint32
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

func (c *Workflow) sendWindow() uint16 {
	return uint16(atomic.LoadInt32(&c.sendWnd))
}

func (c *Workflow) setSendWindow(v uint16) {
	atomic.StoreInt32(&c.sendWnd, int32(v))
}

func (c *Workflow) receiveWindow() uint16 {
	return uint16(atomic.LoadInt32(&c.rcvWnd))
}

func (c *Workflow) setReceiveWindow(v uint16) {
	atomic.StoreInt32(&c.rcvWnd, int32(v))
}

func NewWorkflow(socksDialer proxy.ContextDialer, toTunCh chan<- interface{}, id ip.ConnID, remove func()) *Workflow {
	return &Workflow{
		id:          id,
		remove:      remove,
		socksDialer: socksDialer,
		toTun:       toTunCh,
		fromTun:     make(chan *Packet, ioChannelSize),
		toSocks:     make(chan *Packet, ioChannelSize),
		closeCh:     make(chan error, 5),
		sendWnd:     int32(maxSendWindow),
		rcvWnd:      int32(maxReceiveWindow),
		wfState:     stateIdle,
	}
}

func (c *Workflow) NewPacket(ctx context.Context, pkt *Packet) {
	select {
	case <-ctx.Done():
		c.closeCh <- errQuitByContext
	case c.fromTun <- pkt:
	}
}

func (c *Workflow) Run(ctx context.Context, wg *sync.WaitGroup) {
	defer func() {
		if c.socksConn != nil {
			c.socksConn.Close()
		}
		c.remove()
		wg.Done()
	}()
	c.processPackets(ctx)
}

func (c *Workflow) Close(ctx context.Context) {
	select {
	case <-ctx.Done():
	case c.closeCh <- errQuitByUs:
	}
}

func (c *Workflow) adjustReceiveWindow() {
	queueSize := len(c.toSocks)
	windowSize := maxReceiveWindow
	if queueSize > ioChannelSize/4 {
		windowSize -= queueSize * (maxReceiveWindow / ioChannelSize)
	}
	c.setReceiveWindow(uint16(windowSize))
}

func (c *Workflow) sendToSocks(ctx context.Context, pkt *Packet) bool {
	payloadLen := uint32(len(pkt.Header().Payload()))
	select {
	case c.toSocks <- pkt:
		c.addReceiveNextSequence(payloadLen)
		c.adjustReceiveWindow()
		c.ack(ctx)
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
	pkt := NewPacket(ipPlayloadLen, c.id.Destination(), c.id.Source())
	ipHdr := pkt.IPHeader()
	ipHdr.SetL4Protocol(unix.IPPROTO_TCP)
	ipHdr.SetChecksum()

	tcpHdr := Header(ipHdr.Payload())
	tcpHdr.SetDataOffset(5)
	tcpHdr.SetSourcePort(c.id.DestinationPort())
	tcpHdr.SetDestinationPort(c.id.SourcePort())
	tcpHdr.SetWindowSize(c.receiveWindow())
	return pkt
}

func (c *Workflow) synAck(ctx context.Context, syn *Packet) {
	synHdr := syn.Header()
	if !synHdr.SYN() {
		return
	}
	hl := HeaderLen
	if synHdr.ECE() {
		hl += 4
	}
	pkt := c.newPacket(hl)
	tcpHdr := pkt.Header()
	tcpHdr.SetSYN(true)
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(c.nextSequence())
	tcpHdr.SetAckNumber(synHdr.Sequence() + 1)
	if synHdr.ECE() {
		tcpHdr.SetDataOffset(6)
		opts := tcpHdr.OptionBytes()
		opts[0] = 2
		opts[1] = 4
		binary.BigEndian.PutUint16(opts[2:], uint16(buffer.DataPool.MTU-HeaderLen))
	}
	tcpHdr.SetChecksum(pkt.IPHeader())
	c.sendToTun(ctx, pkt)
	c.addNextSequence(1)
}

func (c *Workflow) finAck(ctx context.Context) {
	pkt := c.newPacket(HeaderLen)
	tcpHdr := pkt.Header()
	tcpHdr.SetFIN(true)
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(c.nextSequence())
	tcpHdr.SetAckNumber(c.receiveNextSequence())
	tcpHdr.SetChecksum(pkt.IPHeader())
	c.sendToTun(ctx, pkt)
	c.addNextSequence(1)
}

func (c *Workflow) ack(ctx context.Context) {
	ackNum := c.receiveNextSequence()
	//	if c.receiveNextSequence() == c.lastAck() {
	//		return
	//	}
	pkt := c.newPacket(HeaderLen)
	tcpHdr := pkt.Header()
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(c.nextSequence())
	tcpHdr.SetAckNumber(ackNum)
	tcpHdr.SetChecksum(pkt.IPHeader())
	c.sendToTun(ctx, pkt)
}

func (c *Workflow) socksWriterLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			c.closeCh <- errQuitByContext
			return
		case pkt := <-c.toSocks:
			c.adjustReceiveWindow()
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
			pkt.Release()
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
		if window == 0 {
			// The intended receiver is currently not accepting data. We must
			// wait for the window to increase.
			dlog.Debugf(ctx, "%s TCP window is zero", c.id)
			for window == 0 {
				dtime.SleepWithContext(ctx, 10*time.Microsecond)
				window = c.sendWindow()
			}
		}

		maxRead := int(window)
		if maxRead > buffer.DataPool.MTU-bothHeadersLen {
			maxRead = buffer.DataPool.MTU - bothHeadersLen
		}

		pkt := c.newPacket(HeaderLen + maxRead)
		ipHdr := pkt.IPHeader()
		tcpHdr := pkt.Header()
		n, err := c.socksConn.Read(tcpHdr.Payload())
		if err != nil {
			pkt.Release()
			if ctx.Err() == nil && c.state() < stateFinWait1 {
				c.closeCh <- err
			}
			return
		}
		if n == 0 {
			continue
		}
		ipHdr.SetPayloadLen(n + HeaderLen)
		ipHdr.SetChecksum()

		tcpHdr.SetACK(true)
		tcpHdr.SetPSH(true)
		tcpHdr.SetSequence(c.nextSequence())
		tcpHdr.SetAckNumber(c.receiveNextSequence())
		tcpHdr.SetChecksum(ipHdr)

		c.sendToTun(ctx, pkt)
		c.addNextSequence(uint32(n))

		// Decrease the window size with the bytes that we just sent unless it's already updated
		// from a received package
		window -= window - uint16(n)
		atomic.CompareAndSwapInt32(&c.sendWnd, int32(window), int32(window))
	}
}

var errQuitByOther = errors.New("quitByOther")
var errQuitByUs = errors.New("quitByUs")
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

func (c *Workflow) idle(ctx context.Context, syn *Packet) error {
	defer syn.Release()
	dlog.Debugf(ctx, "stateIdle %s", c.id)
	conn, err := c.dialSocks(ctx)
	if err != nil {
		dlog.Errorf(ctx, "Unable to connect to socks server: %v", err)
		select {
		case <-ctx.Done():
			return errQuitByContext
		case c.toTun <- syn.Reset():
		}
		return errQuitByReset
	}
	c.socksConn = conn

	c.setReceiveNextSequence(syn.Header().Sequence() + 1)
	c.setNextSequence(1)
	c.synAck(ctx, syn)
	c.setState(stateSynReceived)
	return nil
}

func (c *Workflow) validAck(tcpHdr Header) bool {
	return tcpHdr.AckNumber() == c.nextSequence()
}

func (c *Workflow) validSequence(tcpHdr Header) bool {
	return tcpHdr.Sequence() == c.receiveNextSequence()
}

func (c *Workflow) synReceived(ctx context.Context, pkt *Packet) (err error) {
	dlog.Debugf(ctx, "stateSynReceived %s", c.id)
	release := true
	defer func() {
		if release {
			pkt.Release()
		}
	}()

	tcpHdr := pkt.Header()
	if !(c.validSequence(tcpHdr) && c.validAck(tcpHdr)) {
		if !tcpHdr.RST() {
			select {
			case <-ctx.Done():
				err = errQuitByContext
			case c.toTun <- pkt.Reset():
			}
		}
		return err
	}
	if tcpHdr.RST() {
		return errQuitByReset
	}
	if !tcpHdr.ACK() {
		return nil
	}

	c.setState(stateEstablished)
	go c.socksWriterLoop(ctx)
	go c.socksReaderLoop(ctx)

	if len(tcpHdr.Payload()) != 0 && c.sendToSocks(ctx, pkt) {
		release = false
	}
	return nil
}

func (c *Workflow) established(ctx context.Context, pkt *Packet) error {
	dlog.Debugf(ctx, "stateEstablished %s", c.id)
	release := true
	defer func() {
		if release {
			pkt.Release()
		}
	}()

	tcpHdr := pkt.Header()
	if !c.validSequence(tcpHdr) {
		c.ack(ctx)
		return nil
	}
	if tcpHdr.RST() {
		return errQuitByReset
	}
	if !tcpHdr.ACK() {
		return nil
	}

	if len(tcpHdr.Payload()) != 0 && c.sendToSocks(ctx, pkt) {
		release = false
	}
	if tcpHdr.FIN() {
		c.addReceiveNextSequence(1)
		c.finAck(ctx)
		c.setState(stateLastACK)
		return errQuitByOther
	}
	return nil
}

func (c *Workflow) finWait1(ctx context.Context, pkt *Packet) error {
	dlog.Debugf(ctx, "stateFinWait1 %s", c.id)
	release := true
	defer func() {
		if release {
			pkt.Release()
		}
	}()
	tcpHdr := pkt.Header()
	if !c.validSequence(tcpHdr) {
		dlog.Debugf(ctx, "invalid sequence %s. %d != %d", c.id, tcpHdr.Sequence(), c.receiveNextSequence())
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
	}

	if !c.validAck(tcpHdr) {
		// Not ACK of our FIN
		if len(tcpHdr.Payload()) != 0 {
			if c.sendToSocks(ctx, pkt) {
				release = false
			}
		}
	} else {
		dlog.Debugf(ctx, "stateFinWait1 %s no FIN, entering FIN_WAIT_2", c.id)
		c.setState(stateFinWait2)
	}
	return nil
}

func (c *Workflow) finWait2(ctx context.Context, pkt *Packet) error {
	dlog.Debugf(ctx, "stateFinWait2 %s", c.id)
	defer pkt.Release()
	tcpHdr := pkt.Header()
	if !(c.validSequence(tcpHdr) && c.validAck(tcpHdr)) {
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
	c.setState(stateIdle)
	return errTimedWait
}

func (c *Workflow) closing(ctx context.Context, pkt *Packet) error {
	dlog.Debugf(ctx, "stateClosing %s", c.id)
	defer pkt.Release()
	tcpHdr := pkt.Header()
	if !(c.validSequence(tcpHdr) && c.validAck(tcpHdr)) {
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

func (c *Workflow) atLastAck(ctx context.Context, pkt *Packet) error {
	dlog.Debugf(ctx, "stateLastAck %s", c.id)
	defer pkt.Release()
	tcpHdr := pkt.Header()
	if !(c.validSequence(tcpHdr) && c.validAck(tcpHdr)) {
		return nil
	}
	if !tcpHdr.ACK() {
		return nil
	}
	return errTimedWait
}

func (c *Workflow) processPackets(ctx context.Context) {
	for c.processNextPacket(ctx) {
	}
}

func (c *Workflow) processNextPacket(ctx context.Context) bool {
	select {
	case pkt := <-c.fromTun:
		c.setSendWindow(pkt.Header().WindowSize())

		dlog.Debugf(ctx, "<- TUN %s", pkt)

		var end error
		switch c.state() {
		case stateIdle:
			end = c.idle(ctx, pkt)
		case stateSynReceived:
			end = c.synReceived(ctx, pkt)
		case stateEstablished:
			end = c.established(ctx, pkt)
		case stateFinWait1:
			end = c.finWait1(ctx, pkt)
		case stateFinWait2:
			end = c.finWait2(ctx, pkt)
		case stateClosing:
			end = c.closing(ctx, pkt)
		case stateLastACK:
			end = c.atLastAck(ctx, pkt)
		}
		if end != nil {
			c.closeCh <- end
			return true
		}

	case err := <-c.closeCh:
		switch err {
		case errQuitByUs:
			switch c.state() {
			case stateEstablished:
				c.finAck(ctx)
				c.setState(stateFinWait1)
				return true
			}
			return false
		case errQuitByOther:
			return true
		case errTimedWait:
			c.setState(stateIdle)
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			return c.processNextPacket(ctx)
		}
		return false
	case <-ctx.Done():
		return false
	}
	return true
}

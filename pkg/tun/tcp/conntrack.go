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

	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/buf"

	"golang.org/x/net/proxy"
	"golang.org/x/sys/unix"

	"github.com/datawire/dlib/dlog"
	"github.com/telepresenceio/telepresence/v2/pkg/client"
)

type tcpState int32

const (
	// simplified server-side tcp states
	stateClosed      tcpState = 0x0
	stateSynReceived tcpState = 0x1
	stateEstablished tcpState = 0x2
	stateFinWait1    tcpState = 0x3
	stateFinWait2    tcpState = 0x4
	stateClosing     tcpState = 0x5
	stateLastACK     tcpState = 0x6
	stateTimeWait    tcpState = 0x7

	maxReceiveWindow int = 65535
	maxSendWindow    int = 65535
)

func (state tcpState) String() string {
	switch state {
	case stateClosed:
		return "CLOSED"
	case stateSynReceived:
		return "SYN_RCVD"
	case stateEstablished:
		return "ESTABLISHED"
	case stateFinWait1:
		return "FIN_WAIT_1"
	case stateFinWait2:
		return "FIN_WAIT_2"
	case stateClosing:
		return "CLOSING"
	case stateLastACK:
		return "LAST_ACK"
	case stateTimeWait:
		return "TIME_WAIT"
	}
	return ""
}

type ConnTrack struct {
	id          ip.ConnID
	remove      func()
	socksDialer proxy.ContextDialer
	toTunCh     chan<- interface{}
	input       chan *Packet
	fromSocksCh chan []byte
	toSocksCh   chan *Packet
	ackCh       chan struct{}
	closeCh     chan error

	socksConn net.Conn

	// tcp context
	_state tcpState
	// sequence I should use to send next segment
	// also as ack I expect in next received segment
	_nxtSeq uint32
	// sequence I want in next received segment
	_rcvNxtSeq uint32
	// what I have acked
	_lastAck uint32

	// flow control
	_recvWindow int32
	_sendWindow int32
	sendWndCond *sync.Cond
}

func (tt *ConnTrack) state() tcpState {
	return tcpState(atomic.LoadInt32((*int32)(&tt._state)))
}

func (tt *ConnTrack) changeState(nxt tcpState) {
	atomic.StoreInt32((*int32)(&tt._state), int32(nxt))
}

func (tt *ConnTrack) nxtSeq() uint32 {
	return atomic.LoadUint32(&tt._nxtSeq)
}

func (tt *ConnTrack) incNxtSeq(v uint32) {
	atomic.AddUint32(&tt._nxtSeq, v)
}

func (tt *ConnTrack) setNxtSeq(v uint32) {
	atomic.StoreUint32(&tt._nxtSeq, v)
}

func (tt *ConnTrack) rcvNxtSeq() uint32 {
	return atomic.LoadUint32(&tt._rcvNxtSeq)
}

func (tt *ConnTrack) incRcvNxtSeq(v uint32) {
	atomic.AddUint32(&tt._rcvNxtSeq, v)
}

func (tt *ConnTrack) setRcvNxtSeq(v uint32) {
	atomic.StoreUint32(&tt._rcvNxtSeq, v)
}

func (tt *ConnTrack) lastAck() uint32 {
	return atomic.LoadUint32(&tt._lastAck)
}

func (tt *ConnTrack) setLastAck(v uint32) {
	atomic.StoreUint32(&tt._lastAck, v)
}

func (tt *ConnTrack) sendWindow() int32 {
	return atomic.LoadInt32(&tt._sendWindow)
}

func (tt *ConnTrack) setSendWindow(v int32) {
	atomic.StoreInt32(&tt._sendWindow, v)
}

func (tt *ConnTrack) recvWindow() int32 {
	return atomic.LoadInt32(&tt._recvWindow)
}

func (tt *ConnTrack) setRecvWindow(v int32) {
	atomic.StoreInt32(&tt._recvWindow, v)
}

func NewConnTrack(socksDialer proxy.ContextDialer, toTunCh chan<- interface{}, id ip.ConnID, remove func()) *ConnTrack {
	return &ConnTrack{
		id:          id,
		remove:      remove,
		socksDialer: socksDialer,
		toTunCh:     toTunCh,
		input:       make(chan *Packet, 0x400),
		fromSocksCh: make(chan []byte, 0x400),
		toSocksCh:   make(chan *Packet, 0x400),
		ackCh:       make(chan struct{}, 0x40),
		closeCh:     make(chan error, 5),

		_sendWindow: int32(maxSendWindow),
		_recvWindow: int32(maxReceiveWindow),
		sendWndCond: &sync.Cond{L: &sync.Mutex{}},
		_state:      stateClosed,
	}
}

func (tt *ConnTrack) NewPacket(c context.Context, pkt *Packet) {
	select {
	case <-c.Done():
		tt.closeCh <- errQuitByContext
	case tt.input <- pkt:
	}
}

func (tt *ConnTrack) Close(_ context.Context) {
	tt.closeCh <- errQuitByOther
}

func (tt *ConnTrack) validAck(pkt *Packet) bool {
	return pkt.Header().AckNumber() == tt.nxtSeq()
}

func (tt *ConnTrack) validSeq(pkt *Packet) bool {
	return pkt.Header().Sequence() == tt.rcvNxtSeq()
}

func (tt *ConnTrack) sendToSocks(c context.Context, pkt *Packet) bool {
	payloadLen := uint32(len(pkt.Header().Payload()))
	select {
	case tt.toSocksCh <- pkt:
		tt.incRcvNxtSeq(payloadLen)
		tt.ackCh <- struct{}{}

		// reduce window when recved
		wnd := tt.recvWindow()
		wnd -= int32(payloadLen)
		if wnd < 0 {
			wnd = 0
		}
		tt.setRecvWindow(wnd)

		return true
	case <-c.Done():
		tt.closeCh <- errQuitByContext
		return false
	}
}

func (tt *ConnTrack) sendToTun(c context.Context, pkt *Packet) {
	tcpHdr := pkt.Header()
	if tcpHdr.ACK() {
		tt.setLastAck(tcpHdr.AckNumber())
	}
	select {
	case <-c.Done():
		tt.closeCh <- errQuitByContext
	case tt.toTunCh <- pkt:
	}
}

func (tt *ConnTrack) newTCPPacket(ipPlayloadLen int) *Packet {
	pkg := NewIPPacket(ipPlayloadLen, tt.id.Destination(), tt.id.Source())
	ipHdr := pkg.IPHeader
	ipHdr.SetL4Protocol(unix.IPPROTO_TCP)
	ipHdr.SetChecksum()

	tcpHdr := Header(ipHdr.Payload())
	tcpHdr.SetDataOffset(5)
	tcpHdr.SetSourcePort(tt.id.DestinationPort())
	tcpHdr.SetDestinationPort(tt.id.SourcePort())
	tcpHdr.SetWindowSize(uint16(tt.recvWindow()))
	return pkg
}

func (tt *ConnTrack) synAck(c context.Context, syn *Packet) {
	syntcpHdr := syn.Header()
	if !syntcpHdr.SYN() {
		return
	}
	hl := HeaderLen
	if syntcpHdr.ECE() {
		hl += 4
	}
	pkg := tt.newTCPPacket(hl)
	tcpHdr := pkg.Header()
	tcpHdr.SetSYN(true)
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(tt.nxtSeq())
	tcpHdr.SetAckNumber(syntcpHdr.Sequence() + 1)
	if syntcpHdr.ECE() {
		tcpHdr.SetDataOffset(6)
		opts := tcpHdr.OptionBytes()
		opts[0] = 2
		opts[1] = 4
		binary.BigEndian.PutUint16(opts[2:], buf.MTU-uint16(pkg.IPHeader.HeaderLen()+HeaderLen))
	}
	tcpHdr.SetChecksum(pkg.IPHeader)
	tt.sendToTun(c, pkg)

	// SYN counts 1 seq
	tt.incNxtSeq(1)
}

func (tt *ConnTrack) finAck(c context.Context) {
	pkg := tt.newTCPPacket(HeaderLen)
	tcpHdr := pkg.Header()
	tcpHdr.SetFIN(true)
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(tt.nxtSeq())
	tcpHdr.SetAckNumber(tt.rcvNxtSeq())
	tcpHdr.SetChecksum(pkg.IPHeader)
	tt.sendToTun(c, pkg)
	// FIN counts 1 seq
	tt.incNxtSeq(1)
}

func (tt *ConnTrack) ack(c context.Context) {
	ackNum := tt.rcvNxtSeq()
	if tt.rcvNxtSeq() == tt.lastAck() {
		return
	}
	pkg := tt.newTCPPacket(HeaderLen)
	tcpHdr := pkg.Header()
	tcpHdr.SetACK(true)
	tcpHdr.SetSequence(tt.nxtSeq())
	tcpHdr.SetAckNumber(ackNum)
	tcpHdr.SetChecksum(pkg.IPHeader)
	tt.sendToTun(c, pkg)
}

func (tt *ConnTrack) payload(c context.Context, data []byte) {
	pkg := tt.newTCPPacket(HeaderLen + len(data))
	tcpHdr := pkg.Header()
	tcpHdr.SetACK(true)
	tcpHdr.SetPSH(true)
	tcpHdr.SetSequence(tt.nxtSeq())
	tcpHdr.SetAckNumber(tt.rcvNxtSeq())
	copy(tcpHdr.Payload(), data)
	tcpHdr.SetChecksum(pkg.IPHeader)

	tt.sendToTun(c, pkg)
	// adjust seq
	tt.incNxtSeq(uint32(len(data)))
}

func (tt *ConnTrack) socksReaderLoop(c context.Context) {
	for {
		select {
		case <-c.Done():
			tt.closeCh <- errQuitByContext
			return
		case pkt := <-tt.toSocksCh:
			tcpHdr := pkt.Header()
			dlog.Debugf(c, "socksConn Write: %s", pkt)
			data := tcpHdr.Payload()
			for len(data) > 0 {
				n, err := tt.socksConn.Write(data)
				if err != nil {
					if c.Err() == nil {
						dlog.Error(c, err)
					}
					return
				}
				data = data[n:]
			}

			// increase window when processed
			wnd := tt.recvWindow()
			wnd += int32(len(tcpHdr.Payload()))
			if wnd > int32(maxReceiveWindow) {
				wnd = int32(maxReceiveWindow)
			}
			tt.setRecvWindow(wnd)
			buf.DataPool.PutBuffer(pkt.MTUBuf)
		}
	}
}

func (tt *ConnTrack) socksWriterLoop(c context.Context) {
	for {
		var data [buf.MTU - 40]byte
		var wnd int32
		var cur int32
		wnd = tt.sendWindow()

		if wnd <= 0 {
			for wnd <= 0 {
				tt.sendWndCond.L.Lock()
				tt.sendWndCond.Wait()
				wnd = tt.sendWindow()
			}
			tt.sendWndCond.L.Unlock()
		}

		cur = wnd
		if cur > buf.MTU-40 {
			cur = buf.MTU - 40
		}

		n, err := tt.socksConn.Read(data[:cur])
		if err != nil {
			if c.Err() == nil && tt.state() < stateFinWait1 {
				tt.closeCh <- err
			}
			return
		}
		dlog.Debugf(c, "socksConn Read %d", n)
		b := make([]byte, n)
		copy(b, data[:n])
		tt.fromSocksCh <- b

		nxt := wnd - int32(n)
		if nxt < 0 {
			nxt = 0
		}
		// if sendWindow does not equal to wnd, it is already updated by a
		// received pkt from TUN
		atomic.CompareAndSwapInt32(&tt._sendWindow, wnd, nxt)
	}
}

var errQuitByOther = errors.New("quitByOther")
var errTimedWait = errors.New("quitBySelf")
var errQuitByReset = errors.New("quitByReset")
var errQuitByContext = errors.New("quitByContext")

func (tt *ConnTrack) dialLocalSocks(c context.Context) (net.Conn, error) {
	dlog.Debugf(c, "SOCKS5 DialContext %s", tt.id)

	tos := &client.GetConfig(c).Timeouts
	tc, cancel := context.WithTimeout(c, tos.ProxyDial)
	defer cancel()
	conn, err := tt.socksDialer.DialContext(tc, "tcp", fmt.Sprintf("%s:%d", tt.id.Destination(), tt.id.DestinationPort()))
	if err != nil {
		return nil, client.CheckTimeout(tc, &tos.ProxyDial, fmt.Errorf("fail to connect SOCKS proxy: %v", err))
	}
	return conn, nil
}

// stateClosed receives a SYN packet, tries to connect the socks proxy, gives a
// SYN/ACK if success, otherwise RST
func (tt *ConnTrack) stateClosed(c context.Context, syn *Packet) (end error, release bool) {
	dlog.Debugf(c, "stateClosed %s.%d", tt.id.Destination(), tt.id.DestinationPort())
	for i := 0; i < 2; i++ {
		conn, err := tt.dialLocalSocks(c)
		if err != nil {
			dlog.Error(c, err)
			continue
		}
		tt.socksConn = conn
		break
	}

	if tt.socksConn == nil {
		select {
		case <-c.Done():
			return errQuitByContext, true
		case tt.toTunCh <- syn.Reset():
		}
		return errQuitByReset, true
	}
	// context variables
	tt.setRcvNxtSeq(syn.Header().Sequence() + 1)
	tt.setNxtSeq(1)

	tt.synAck(c, syn)
	tt.changeState(stateSynReceived)
	return nil, true
}

// stateSynReceived expects a ACK with matching ack number,
func (tt *ConnTrack) stateSynReceived(c context.Context, pkt *Packet) (end error, release bool) {
	dlog.Debugf(c, "stateSynReceived %s", tt.id)
	// rst to packet with invalid sequence/ack, state unchanged
	tcpHdr := pkt.Header()
	if !(tt.validSeq(pkt) && tt.validAck(pkt)) {
		if !tcpHdr.RST() {
			select {
			case <-c.Done():
				return errQuitByContext, true
			case tt.toTunCh <- pkt.Reset():
			}
		}
		return nil, true
	}
	// connection ends by valid RST
	if tcpHdr.RST() {
		return errQuitByReset, true
	}
	// ignore non-ACK packets
	if !tcpHdr.ACK() {
		return nil, true
	}

	release = true
	tt.changeState(stateEstablished)
	go tt.socksReaderLoop(c)
	go tt.socksWriterLoop(c)

	if len(tcpHdr.Payload()) != 0 {
		if tt.sendToSocks(c, pkt) {
			// pkt hands to socks writer
			release = false
		}
	}
	return nil, release
}

func (tt *ConnTrack) stateEstablished(c context.Context, pkt *Packet) (end error, release bool) {
	dlog.Debugf(c, "stateEstablished %s", tt.id)
	// ack if sequence is not expected
	if !tt.validSeq(pkt) {
		tt.ack(c)
		return nil, true
	}
	tcpHdr := pkt.Header()

	// connection ends by valid RST
	if tcpHdr.RST() {
		return errQuitByReset, true
	}
	// ignore non-ACK packets
	if !tcpHdr.ACK() {
		return nil, true
	}

	release = true
	if len(tcpHdr.Payload()) != 0 {
		if tt.sendToSocks(c, pkt) {
			// pkt hands to socks writer
			release = false
		}
	}
	if tcpHdr.FIN() {
		tt.incRcvNxtSeq(1)
		tt.finAck(c)
		tt.changeState(stateLastACK)
		return errQuitByOther, release
	}
	return nil, release
}

func (tt *ConnTrack) stateFinWait1(c context.Context, pkt *Packet) (end error, release bool) {
	dlog.Debugf(c, "stateFinWait1 %s", tt.id)
	// ignore packet with invalid sequence, state unchanged
	if !tt.validSeq(pkt) {
		return nil, true
	}
	tcpHdr := pkt.Header()
	// connection ends by valid RST
	if tcpHdr.RST() {
		return errQuitByReset, true
	}
	// ignore non-ACK packets
	if !tcpHdr.ACK() {
		return nil, true
	}

	if tcpHdr.FIN() {
		tt.incRcvNxtSeq(1)
		tt.ack(c)
		if tt.validAck(pkt) {
			return errTimedWait, true
		} else {
			tt.changeState(stateClosing)
			return nil, true
		}
	} else {
		tt.changeState(stateFinWait2)
		return nil, true
	}
}

func (tt *ConnTrack) stateFinWait2(c context.Context, pkt *Packet) (end error, release bool) {
	dlog.Debugf(c, "stateFinWait2 %s", tt.id)
	// ignore packet with invalid sequence/ack, state unchanged
	if !(tt.validSeq(pkt) && tt.validAck(pkt)) {
		return nil, true
	}
	tcpHdr := pkt.Header()
	// connection ends by valid RST
	if tcpHdr.RST() {
		return errQuitByReset, true
	}
	// ignore non-ACK packets
	if !tcpHdr.ACK() {
		return nil, true
	}
	tt.incRcvNxtSeq(1)
	tt.ack(c)
	tt.changeState(stateClosed)
	return errTimedWait, true
}

func (tt *ConnTrack) stateClosing(c context.Context, pkt *Packet) (end error, release bool) {
	dlog.Debugf(c, "stateClosing %s", tt.id)
	// ignore packet with invalid sequence/ack, state unchanged
	if !(tt.validSeq(pkt) && tt.validAck(pkt)) {
		return nil, true
	}
	tcpHdr := pkt.Header()
	// connection ends by valid RST
	if tcpHdr.RST() {
		return errQuitByReset, true
	}
	// ignore non-ACK packets
	if !tcpHdr.ACK() {
		return nil, true
	}
	return errTimedWait, true
}

func (tt *ConnTrack) stateLastAck(c context.Context, pkt *Packet) (end error, release bool) {
	dlog.Debugf(c, "stateLastAck %s", tt.id)
	// ignore packet with invalid sequence/ack, state unchanged
	if !(tt.validSeq(pkt) && tt.validAck(pkt)) {
		return nil, true
	}
	// ignore non-ACK packets
	if !pkt.Header().ACK() {
		return nil, true
	}
	// connection ends
	return errTimedWait, true
}

func (tt *ConnTrack) updateSendWindow(pkt *Packet) {
	tt.setSendWindow(int32(pkt.Header().WindowSize()))
	tt.sendWndCond.Signal()
}

func (tt *ConnTrack) Run(c context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	go tt.runAcker(c)
	go tt.runSocksForwarder(c)
	for tt.transitionState(c) {
	}
}

func (tt *ConnTrack) transitionState(c context.Context) bool {
	select {
	case pkt := <-tt.input:
		dlog.Debugf(c, "read from TUN %s", pkt)

		var end error
		var release bool
		tt.updateSendWindow(pkt)
		switch tt.state() {
		case stateClosed:
			end, release = tt.stateClosed(c, pkt)
		case stateSynReceived:
			end, release = tt.stateSynReceived(c, pkt)
		case stateEstablished:
			end, release = tt.stateEstablished(c, pkt)
		case stateFinWait1:
			end, release = tt.stateFinWait1(c, pkt)
		case stateFinWait2:
			end, release = tt.stateFinWait2(c, pkt)
		case stateClosing:
			end, release = tt.stateClosing(c, pkt)
		case stateLastACK:
			end, release = tt.stateLastAck(c, pkt)
		}
		if release {
			buf.DataPool.PutBuffer(pkt.MTUBuf)
		}
		if end != nil {
			tt.closeCh <- end
			return true
		}

	case err := <-tt.closeCh:
		switch err {
		case errQuitByOther:
			tt.finAck(c)
			tt.changeState(stateFinWait1)
			return true
		case errTimedWait:
			tt.changeState(stateClosed)
			c, cancel := context.WithTimeout(c, time.Second)
			defer cancel()
			return tt.transitionState(c)
		}
		tt.remove()
		tt.socksConn.Close()
		return false

	case <-c.Done():
		tt.remove()
		tt.socksConn.Close()
		return false
	}
	return true
}

func (tt *ConnTrack) runSocksForwarder(c context.Context) {
	for {
		select {
		case <-c.Done():
			return
		case data := <-tt.fromSocksCh:
			tt.payload(c, data)
		}
	}
}
func (tt *ConnTrack) runAcker(c context.Context) {
	for {
		select {
		case <-c.Done():
			return
		case <-tt.ackCh:
			tt.ack(c)
		}
	}
}

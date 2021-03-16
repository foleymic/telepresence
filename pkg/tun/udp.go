package tun

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/tcp"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/buf"

	"github.com/telepresenceio/telepresence/v2/pkg/tun/ip"

	"golang.org/x/net/ipv4"

	"golang.org/x/sys/unix"
)

const udpHeaderLen = 8

// The UDP datagram and its payload
type udpDatagram []byte

func (u udpDatagram) setValues(src, dst uint16, payloadLen int) {
	binary.BigEndian.PutUint16(u, src)
	binary.BigEndian.PutUint16(u[2:], dst)
	binary.BigEndian.PutUint16(u[4:], uint16(udpHeaderLen+payloadLen))
}

func (u udpDatagram) source() int {
	return int(binary.BigEndian.Uint16(u))
}

func (u udpDatagram) destination() int {
	return int(binary.BigEndian.Uint16(u[2:]))
}

func (u udpDatagram) length() int {
	return int(binary.BigEndian.Uint16(u[4:]))
}

func (u udpDatagram) checksum() uint16 {
	return binary.BigEndian.Uint16(u[6:])
}

func (u udpDatagram) setChecksum(ipHdr ip.Header, payload []byte) {
	ip.L4Checksum(ipHdr, 6, unix.IPPROTO_UDP)
}

func (u udpDatagram) body() []byte {
	return u[udpHeaderLen:]
}

func (u udpDatagram) String() string {
	return fmt.Sprintf("src=%d, dst=%d, length=%d, crc=%d", u.source(), u.destination(), u.length()-udpHeaderLen, u.checksum())
}

type udpHandler struct {
	dispatcher *Dispatcher
	id         ip.ConnID
	conn       *net.IPConn
}

type udpPacket []byte

func (d *Device) sendIPv4ResponseUDP(local net.IP, remote net.IP, lPort uint16, rPort uint16, payload []byte) error {
	packageID := tcp.NextID()
	mtuBuf := buf.DataPool.GetBuffer(ipv4.HeaderLen + udpHeaderLen + len(payload))
	ipHdr := ip.V4Header(mtuBuf.Buf())
	ipHdr.Initialize()
	ipHdr.SetID(packageID)
	ipHdr.SetSource(remote)
	ipHdr.SetDestination(local)
	ipHdr.SetL4Protocol(unix.IPPROTO_UDP)

	udp := udpDatagram(ipHdr.Payload())
	udp.setValues(rPort, lPort, len(payload))
	udp.setChecksum(ipHdr, payload)

	return d.sendIPv4Fragments(mtuBuf, udpHeaderLen, payload)
}

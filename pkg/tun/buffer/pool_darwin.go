package buffer

import (
	"sync"
)

const PrefixLen = 4

// Data on a MacOS consists of two slices that share the same underlying byte array. The
// raw data points to the beginning of the array and the buf points PrefixLen into the array.
// All data manipulation is then done using the buf, except reads/writes to the tun device which
// uses the raw. This setup enables the read/write to receive and write the required 4-byte
// header that MacOS TUN socket uses without copying data.
type Data struct {
	buf []byte
	raw []byte
}

func (b *Data) Buf() []byte {
	return b.buf
}

func (b *Data) SetLength(l int) {
	if l > cap(b.buf) {
		raw := b.raw
		b.raw = make([]byte, l+PrefixLen)
		copy(b.raw, raw)
		b.buf = b.raw[PrefixLen:]
	} else {
		b.buf = b.buf[:l]
		b.raw = b.raw[:l+PrefixLen]
	}
}

func (b *Data) Slice(start, end int) *Data {
	raw := b.raw[PrefixLen+start : PrefixLen+end]
	return &Data{buf: raw[PrefixLen:], raw: raw}
}

func (b *Data) Raw() []byte {
	return b.raw
}

var DataPool = &Pool{
	pool: sync.Pool{
		New: func() interface{} {
			raw := make([]byte, PrefixLen+defaultMTU+maxIPHeader)
			return &Data{buf: raw[PrefixLen:], raw: raw}
		}},
	MTU: defaultMTU,
}

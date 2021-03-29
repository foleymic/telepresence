package buffer

import (
	"sync"
)

const PrefixLen = 4

type Data struct {
	buf []byte
	raw []byte
}

func (b *Data) Buf() []byte {
	return b.buf
}

func (b *Data) SetLength(l int) {
	if l > cap(b.buf) {
		b.raw = make([]byte, l+PrefixLen)
		b.buf = b.raw[PrefixLen:]
	} else {
		b.buf = b.buf[:l]
		b.raw = b.raw[:l+PrefixLen]
	}
}

func (b *Data) Append(o *Data) *Data {
	raw := make([]byte, len(b.raw)+len(o.buf))
	copy(raw, b.raw)
	copy(raw[len(b.raw):], o.buf)
	return &Data{buf: raw[PrefixLen:], raw: raw}
}

func (b *Data) Copy() *Data {
	raw := make([]byte, len(b.raw))
	copy(raw, b.raw)
	return &Data{buf: raw[PrefixLen:], raw: raw}
}

func (b *Data) Slice(start, end int) *Data {
	raw := b.raw[PrefixLen+start : PrefixLen+end]
	return &Data{buf: raw[PrefixLen:], raw: raw}
}

func (b *Data) Raw() []byte {
	return b.raw
}

var DataPool = &Pool{Pool: sync.Pool{
	New: func() interface{} {
		raw := make([]byte, PrefixLen+Size)
		return &Data{buf: raw[PrefixLen:], raw: raw}
	}}}

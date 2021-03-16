package buf

import (
	"sync"
)

const PrefixLen = 4

type Buffer struct {
	buf []byte
	raw []byte
}

func (b *Buffer) Buf() []byte {
	return b.buf
}

func (b *Buffer) SetLength(l int) {
	if l > cap(b.buf) {
		b.raw = make([]byte, l+PrefixLen)
		b.buf = b.raw[PrefixLen:]
	} else {
		b.buf = b.buf[:l]
		b.raw = b.raw[:l+PrefixLen]
	}
}

func (b *Buffer) Append(o *Buffer) *Buffer {
	raw := make([]byte, len(b.raw)+len(o.buf))
	copy(raw, b.raw)
	copy(raw[len(b.raw):], o.buf)
	return &Buffer{buf: raw[PrefixLen:], raw: raw}
}

func (b *Buffer) Copy() *Buffer {
	raw := make([]byte, len(b.raw))
	copy(raw, b.raw)
	return &Buffer{buf: raw[PrefixLen:], raw: raw}
}

func (b *Buffer) Slice(start, end int) *Buffer {
	raw := b.raw[PrefixLen+start : PrefixLen+end]
	return &Buffer{buf: raw[PrefixLen:], raw: raw}
}

func (b *Buffer) Raw() []byte {
	return b.raw
}

var DataPool = &Pool{Pool: sync.Pool{
	New: func() interface{} {
		raw := make([]byte, PrefixLen+Size)
		return &Buffer{buf: raw[PrefixLen:], raw: raw}
	}}}

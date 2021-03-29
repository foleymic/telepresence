package buffer

import (
	"sync"
)

type Buffer struct {
	buf []byte
}

func (b *Buffer) Buf() []byte {
	return b.buf
}

func (b *Buffer) Slice(start, end int) *Buffer {
	return &Buffer{buf: b.buf[start:end]}
}

func (b *Buffer) Raw() []byte {
	return b.buf
}

func (b *Buffer) SetLength(l int) {
	if l > cap(b.buf) {
		b.buf = make([]byte, l)
	} else {
		b.buf = b.buf[:l]
	}
}

func (b *Buffer) Copy() *Buffer {
	raw := make([]byte, len(b.raw))
	copy(raw, b.raw)
	return &Buffer{raw: raw}
}

var BufferPool = &buf.OffsetBufPool{Pool: sync.Pool{
	New: func() interface{} {
		return &Buffer{buf: make([]byte, Size)}
	}}}

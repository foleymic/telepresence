package buffer

import (
	"sync"
)

type Data struct {
	buf []byte
}

func (b *Data) Buf() []byte {
	return b.buf
}

func (b *Data) SetLength(l int) {
	if l > cap(b.buf) {
		buf := b.buf
		b.buf = make([]byte, l)
		copy(b.buf, buf)
	} else {
		b.buf = b.buf[:l]
	}
}

func (b *Data) Slice(start, end int) *Data {
	return &Data{buf: b.buf[start:end]}
}

func (b *Data) Raw() []byte {
	return b.buf
}

var DataPool = &Pool{
	pool: sync.Pool{
		New: func() interface{} {
			return &Data{buf: make([]byte, defaultMTU+maxIPHeader)}
		}},
	MTU: defaultMTU,
}

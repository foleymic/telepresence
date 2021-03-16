package buf

import (
	"sync"

	"golang.org/x/net/ipv6"
)

const MTU = 1500
const Size = ipv6.HeaderLen + 60 + MTU

type Pool struct {
	sync.Pool
}

func (p *Pool) GetBuffer(size int) *Buffer {
	b := p.Get().(*Buffer)
	b.SetLength(size)
	return b
}

func (p *Pool) PutBuffer(b *Buffer) {
	p.Put(b)
}

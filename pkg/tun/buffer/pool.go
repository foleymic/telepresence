package buffer

import (
	"sync"

	"golang.org/x/net/ipv6"
)

const MTU = 1500
const Size = ipv6.HeaderLen + 60 + MTU

type Pool struct {
	sync.Pool
}

func (p *Pool) GetData(size int) *Data {
	b := p.Get().(*Data)
	b.SetLength(size)
	return b
}

func (p *Pool) PutBuffer(b *Data) {
	p.Put(b)
}

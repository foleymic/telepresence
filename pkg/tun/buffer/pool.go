package buffer

import (
	"sync"
)

const defaultMTU = 1500
const maxIPHeader = 60

type Pool struct {
	pool sync.Pool
	MTU  int
}

func (p *Pool) Get(size int) *Data {
	b := p.pool.Get().(*Data)
	b.SetLength(size)
	return b
}

func (p *Pool) Put(b *Data) {
	p.pool.Put(b)
}

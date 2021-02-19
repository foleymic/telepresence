//+build ignore

package setonce

import (
	"context"
	"sync"

	"VALPKG"
)

type SETTYPE interface {
	Get(ctx context.Context) (val VALTYPE, ok bool)
	Set(val VALTYPE)
}

type _SETTYPE struct {
	val   VALTYPE
	panic bool
	once  sync.Once
	ch    chan struct{}
}

func NewSETTYPE(second Behavior) SETTYPE {
	return &_SETTYPE{
		ch:    make(chan struct{}),
		panic: second.isPanic(),
	}
}

func (mu *_SETTYPE) Set(val VALTYPE) {
	didSet := false
	mu.once.Do(func() {
		mu.val = val
		close(mu.ch)
		didSet = true
	})
	if mu.panic && !didSet {
		panic("setonce.SETTYPE.Set called multiple times")
	}
}

func (mu *_SETTYPE) Get(ctx context.Context) (val VALTYPE, ok bool) {
	select {
	case <-mu.ch:
		val = mu.val
		ok = true
	case <-ctx.Done():
		ok = false
	}
	return
}

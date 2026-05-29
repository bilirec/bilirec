package tx

import (
	"sync"

	"github.com/bilirec/bilirec/pkg/ds"
)

type Pending[T comparable] struct {
	validateFunc func(key T, pending ds.Set[T]) error
	pending      ds.Set[T]
	mu           sync.Mutex
}

type PendingTxn[T comparable] struct {
	*Pending[T]
	reserved bool
	closed   bool
	key      T
}

func NewPending[T comparable](validateFunc func(key T, pending ds.Set[T]) error) Coordinator[T] {
	return &Pending[T]{
		validateFunc: validateFunc,
		pending:      ds.NewSyncedSet[T](),
	}
}

func (g *Pending[T]) Begin() Txn[T] {
	return &PendingTxn[T]{
		Pending: g,
	}
}

func (g *Pending[T]) Run(key T, fn func() error) error {
	return g.RunWithHooks(key, fn, func() {}, func() {})
}

func (g *Pending[T]) RunWithConfirm(key T, fn func() error, confirm func()) error {
	return g.RunWithHooks(key, fn, func() {}, confirm)
}

func (g *Pending[T]) RunWithAbort(key T, fn func() error, abort func()) error {
	return g.RunWithHooks(key, fn, abort, func() {})
}

func (g *Pending[T]) RunWithHooks(key T, fn func() error, abort func(), confirm func()) error {
	return g.Begin().RunWithHooks(key, fn, abort, confirm)
}

func (g *PendingTxn[T]) Reserve(key T) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrTxnClosed
	}
	if g.reserved {
		return ErrAlreadyReserved
	}
	if g.pending.Add(key) {
		return ErrAlreadyReserved
	}
	if err := g.validateFunc(key, g.pending); err != nil {
		g.pending.Remove(key)
		return err
	}
	g.reserved = true
	g.key = key
	return nil
}

func (g *PendingTxn[T]) Abort(key T) {
	g.AbortWith(key, func() {})
}

func (g *PendingTxn[T]) AbortWith(key T, fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.reserved || g.closed {
		return
	}
	g.pending.Remove(key)
	fn()
	g.reserved = false
	g.closed = true
}

func (g *PendingTxn[T]) Confirm(key T) {
	g.ConfirmWith(key, func() {})
}

func (g *PendingTxn[T]) ConfirmWith(key T, confirm func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.reserved || g.closed {
		return
	}
	g.pending.Remove(key)
	confirm()
	g.reserved = false
	g.closed = true
}

func (g *PendingTxn[T]) Run(key T, fn func() error) error {
	return g.RunWithHooks(key, fn, func() {}, func() {})
}

func (g *PendingTxn[T]) RunWithConfirm(key T, fn func() error, confirm func()) error {
	return g.RunWithHooks(key, fn, func() {}, confirm)
}

func (g *PendingTxn[T]) RunWithAbort(key T, fn func() error, abort func()) error {
	return g.RunWithHooks(key, fn, abort, func() {})
}

func (g *PendingTxn[T]) RunWithHooks(key T, fn func() error, abort func(), confirm func()) error {
	if err := g.Reserve(key); err != nil {
		return err
	}

	defer g.AbortWith(key, abort)

	if err := fn(); err != nil {
		return err
	}

	g.ConfirmWith(key, confirm)

	return nil
}

package tx

import "sync"

type Hook[T comparable] struct {
	reserveFunc func(key T) error
	abortFunc   func(key T)
	confirmFunc func(key T)
	mu          sync.Mutex
}

type HookTxn[T comparable] struct {
	*Hook[T]
	reserved bool
	closed   bool
}

func NewHook[T comparable](reserve func(key T) error, abort func(key T), confirm func(key T)) Coordinator[T] {
	return &Hook[T]{
		reserveFunc: reserve,
		abortFunc:   abort,
		confirmFunc: confirm,
	}
}

func (r *Hook[T]) Begin() Txn[T] {
	return &HookTxn[T]{
		Hook: r,
	}
}

func (r *Hook[T]) Run(key T, fn func() error) error {
	return r.RunWithHooks(key, fn, func() {}, func() {})
}

func (r *Hook[T]) RunWithConfirm(key T, fn func() error, confirm func()) error {
	return r.RunWithHooks(key, fn, func() {}, confirm)
}

func (r *Hook[T]) RunWithAbort(key T, fn func() error, abort func()) error {
	return r.RunWithHooks(key, fn, abort, func() {})
}

func (r *Hook[T]) RunWithHooks(key T, fn func() error, abort func(), confirm func()) error {
	return r.Begin().RunWithHooks(key, fn, abort, confirm)
}

func (r *HookTxn[T]) Reserve(key T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrTxnClosed
	}
	if r.reserved {
		return ErrAlreadyReserved
	}
	if err := r.reserveFunc(key); err != nil {
		return err
	}
	r.reserved = true
	return nil
}

func (r *HookTxn[T]) Abort(key T) {
	r.AbortWith(key, func() {})
}

func (r *HookTxn[T]) AbortWith(key T, fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.reserved || r.closed {
		return
	}
	r.abortFunc(key)
	fn()
	r.reserved = false
	r.closed = true
}

func (r *HookTxn[T]) Confirm(key T) {
	r.ConfirmWith(key, func() {})
}

func (r *HookTxn[T]) ConfirmWith(key T, confirm func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.reserved || r.closed {
		return
	}
	r.confirmFunc(key)
	confirm()
	r.reserved = false
	r.closed = true
}

func (r *HookTxn[T]) Run(key T, fn func() error) error {
	return r.RunWithHooks(key, fn, func() {}, func() {})
}

func (r *HookTxn[T]) RunWithConfirm(key T, fn func() error, confirm func()) error {
	return r.RunWithHooks(key, fn, func() {}, confirm)
}

func (r *HookTxn[T]) RunWithAbort(key T, fn func() error, abort func()) error {
	return r.RunWithHooks(key, fn, abort, func() {})
}

func (r *HookTxn[T]) RunWithHooks(key T, fn func() error, abort func(), confirm func()) error {
	if err := r.Reserve(key); err != nil {
		return err
	}

	defer r.AbortWith(key, abort)

	if err := fn(); err != nil {
		return err
	}

	r.ConfirmWith(key, confirm)

	return nil
}

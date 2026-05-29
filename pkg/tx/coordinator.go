package tx

import "errors"

var ErrTxnClosed = errors.New("tx: transaction is closed")
var ErrAlreadyReserved = errors.New("tx: key is already reserved")

// Coordinator coordinates keyed reservations in a transactional manner.
type Coordinator[T comparable] interface {
	Begin() Txn[T]

	Run(key T, fn func() error) error
	RunWithConfirm(key T, fn func() error, confirm func()) error
	RunWithAbort(key T, fn func() error, abort func()) error
	RunWithHooks(key T, fn func() error, abort func(), confirm func()) error
}

// Txn models a single reservation transaction.
type Txn[T comparable] interface {
	Reserve(key T) error
	Abort(key T)
	AbortWith(key T, fn func())
	Confirm(key T)
	ConfirmWith(key T, fn func())
	Run(key T, fn func() error) error
	RunWithConfirm(key T, fn func() error, confirm func()) error
	RunWithAbort(key T, fn func() error, abort func()) error
	RunWithHooks(key T, fn func() error, abort func(), confirm func()) error
}

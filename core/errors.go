package core

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Domain sentinel errors. These carry no HTTP or bizcode knowledge.
var (
	ErrNotFound            = errors.New("not found")
	ErrInvalidInput        = errors.New("invalid input")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrDuplicateJournal    = errors.New("duplicate journal")
	ErrUnbalancedJournal   = errors.New("unbalanced journal")
	ErrInvalidTransition   = errors.New("invalid state transition")
	ErrConflict            = errors.New("conflict")
)

// ErrOutsideTransaction is returned by an operation that only means
// something inside a transaction (a transaction-scoped lock taken outside
// one would be released at once and protect nothing).
var ErrOutsideTransaction = errors.New("operation requires a transaction")

// NegativeBalanceError reports that a journal posted with
// JournalInput.RequireNonNegative would leave a guarded balance below zero.
// The journal is not posted (its transaction must roll back). It matches
// ErrInsufficientBalance with errors.Is.
type NegativeBalanceError struct {
	Key BalanceKey
	// Balance is the normal-side balance the journal would leave.
	Balance decimal.Decimal
	// Change is the journal's own normal-side effect on that balance, so the
	// balance before it was Balance - Change.
	Change decimal.Decimal
}

func (e *NegativeBalanceError) Error() string {
	return fmt.Sprintf("insufficient balance: holder %d currency %d classification %d would be %s (before %s, change %s)",
		e.Key.AccountHolder, e.Key.CurrencyID, e.Key.ClassificationID, e.Balance, e.Balance.Sub(e.Change), e.Change)
}

// Unwrap makes errors.Is(err, ErrInsufficientBalance) true.
func (e *NegativeBalanceError) Unwrap() error { return ErrInsufficientBalance }

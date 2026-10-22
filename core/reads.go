package core

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// BalanceKey names one balance dimension: (holder, currency, classification).
type BalanceKey struct {
	AccountHolder    int64 `json:"account_holder"`
	CurrencyID       int64 `json:"currency_id"`
	ClassificationID int64 `json:"classification_id"`
}

// BalanceFilter narrows the read-side queries of a LedgerReader.
type BalanceFilter struct {
	// CurrencyID is required: balances in different currencies never mix.
	CurrencyID int64 `json:"currency_id"`
	// ClassificationIDs limits the result to these classifications; empty
	// means every classification.
	ClassificationIDs []int64 `json:"classification_ids"`
	// Before, when non-zero, counts only the entries whose created_at is
	// strictly before it. created_at is the posting transaction's START time
	// (now()), not its commit time: a transaction that began before Before
	// and commits after it adds entries "before" an instant that was already
	// read, so a past position can still change until every transaction
	// that started before it has finished. Entries are append-only and can't
	// be back-dated (migration 022), but this is not a closed-period
	// guarantee; period close is a separate, future mechanism.
	Before time.Time `json:"before"`
}

// Validate checks the filter.
func (f BalanceFilter) Validate() error {
	if f.CurrencyID <= 0 {
		return fmt.Errorf("core: balance filter: currency_id is required: %w", ErrInvalidInput)
	}
	return nil
}

// SubBalance is one (holder, classification) balance in a currency.
// Balance is on the classification's NORMAL side: debits minus credits for a
// debit-normal classification, credits minus debits for a credit-normal one.
// Debits and Credits are the gross sums that produced it.
type SubBalance struct {
	AccountHolder    int64           `json:"account_holder"`
	CurrencyID       int64           `json:"currency_id"`
	ClassificationID int64           `json:"classification_id"`
	NormalSide       NormalSide      `json:"normal_side"`
	Debits           decimal.Decimal `json:"debits"`
	Credits          decimal.Decimal `json:"credits"`
	Balance          decimal.Decimal `json:"balance"`
}

// HolderEntry is one journal entry on a holder's balances, with the
// idempotency key of the journal that posted it.
type HolderEntry struct {
	EntryID          int64           `json:"entry_id"`
	JournalID        int64           `json:"journal_id"`
	IdempotencyKey   string          `json:"idempotency_key"`
	CurrencyID       int64           `json:"currency_id"`
	ClassificationID int64           `json:"classification_id"`
	EntryType        EntryType       `json:"entry_type"`
	Amount           decimal.Decimal `json:"amount"`
	CreatedAt        time.Time       `json:"created_at"`
}

// TrialBalanceLine is one classification's totals across every holder.
// Balance is on the classification's normal side. Across all lines of one
// currency, the debit totals equal the credit totals.
type TrialBalanceLine struct {
	ClassificationID int64           `json:"classification_id"`
	Code             string          `json:"code"`
	NormalSide       NormalSide      `json:"normal_side"`
	Debits           decimal.Decimal `json:"debits"`
	Credits          decimal.Decimal `json:"credits"`
	Balance          decimal.Decimal `json:"balance"`
}

// Reversal-chain violation kinds (see LedgerReader.ReversalChainViolations).
const (
	// ReversalOfReversal: a reversal journal names, as its original, a
	// journal that is itself a reversal. Reversing a reversal is refused;
	// post a fresh journal instead.
	ReversalOfReversal = "reversal_of_reversal"
	// ReversalNotMirror: a reversal's entries are not the exact mirror of
	// its original's (every dimension, side flipped, same amount).
	ReversalNotMirror = "reversal_not_mirror"
)

// ReversalViolation is one broken reversal link.
type ReversalViolation struct {
	Kind              string `json:"kind"`
	ReversalJournalID int64  `json:"reversal_journal_id"`
	OriginalJournalID int64  `json:"original_journal_id"`
}

// LedgerReader is the read side callers need without knowing the schema.
// Implementations bound to a transaction see that transaction's own writes.
type LedgerReader interface {
	// JournalByKey returns the journal posted with this idempotency key,
	// or ErrNotFound.
	JournalByKey(ctx context.Context, idempotencyKey string) (*Journal, error)
	// JournalEntries returns a journal's entries in posting order.
	JournalEntries(ctx context.Context, journalID int64) ([]Entry, error)
	// SubBalances returns a balance per (holder, classification) that has
	// any entries matching the filter.
	SubBalances(ctx context.Context, f BalanceFilter) ([]SubBalance, error)
	// HolderEntries returns every entry on one holder's balances matching
	// the filter, oldest first.
	HolderEntries(ctx context.Context, holder int64, f BalanceFilter) ([]HolderEntry, error)
	// TrialBalance returns per-classification totals across every holder.
	TrialBalance(ctx context.Context, f BalanceFilter) ([]TrialBalanceLine, error)
	// ReversalChainViolations scans every reversal link (journals with
	// reversal_of set) and reports the ones that break the chain rules: a
	// journal is reversed at most once (also a unique index), a reversal is
	// never itself reversed, and a reversal exactly mirrors its original.
	ReversalChainViolations(ctx context.Context) ([]ReversalViolation, error)
}

// ReverseInput describes a full reversal: a journal that mirrors the
// original's entries with every side flipped, linked by reversal_of.
type ReverseInput struct {
	JournalID int64 `json:"journal_id"`
	// IdempotencyKey of the reversal. Empty: "reversal:<journal_id>:<reason>"
	// (ReverseJournal's key).
	IdempotencyKey string `json:"idempotency_key"`
	// Reason is recorded in the reversal's metadata ("reason") when set.
	Reason string `json:"reason"`
	// Source of the reversal journal. Empty: "reversal".
	Source   string            `json:"source"`
	ActorID  int64             `json:"actor_id"`
	Metadata map[string]string `json:"metadata"`
	// RequireNonNegative is passed through to the reversal's PostJournal.
	RequireNonNegative []BalanceKey `json:"require_non_negative"`
}

// Validate checks the input.
func (r ReverseInput) Validate() error {
	if r.JournalID <= 0 {
		return fmt.Errorf("core: reverse journal: journal_id is required: %w", ErrInvalidInput)
	}
	return nil
}

// JournalReverser posts full reversals with caller-chosen keys.
type JournalReverser interface {
	ReverseJournalWith(ctx context.Context, in ReverseInput) (*Journal, error)
}

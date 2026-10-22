package core

import (
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// Journal is a persisted balanced journal.
type Journal struct {
	ID             int64             `json:"id"`
	JournalTypeID  int64             `json:"journal_type_id"`
	IdempotencyKey string            `json:"idempotency_key"`
	TotalDebit     decimal.Decimal   `json:"total_debit"`
	TotalCredit    decimal.Decimal   `json:"total_credit"`
	Metadata       map[string]string `json:"metadata"`
	ActorID        int64             `json:"actor_id"`
	Source         string            `json:"source"`
	ReversalOf     int64             `json:"reversal_of"`
	EventID        int64             `json:"event_id"`
	CreatedAt      time.Time         `json:"created_at"`
}

// Entry is a single debit or credit line in a journal.
type Entry struct {
	ID               int64           `json:"id"`
	JournalID        int64           `json:"journal_id"`
	AccountHolder    int64           `json:"account_holder"`
	CurrencyID       int64           `json:"currency_id"`
	ClassificationID int64           `json:"classification_id"`
	EntryType        EntryType       `json:"entry_type"`
	Amount           decimal.Decimal `json:"amount"`
	CreatedAt        time.Time       `json:"created_at"`
}

// EntryInput is the input for a single entry line.
type EntryInput struct {
	AccountHolder    int64           `json:"account_holder"`
	CurrencyID       int64           `json:"currency_id"`
	ClassificationID int64           `json:"classification_id"`
	EntryType        EntryType       `json:"entry_type"`
	Amount           decimal.Decimal `json:"amount"`
}

// JournalInput is the input to post a journal.
type JournalInput struct {
	JournalTypeID  int64             `json:"journal_type_id"`
	IdempotencyKey string            `json:"idempotency_key"`
	Entries        []EntryInput      `json:"entries"`
	Metadata       map[string]string `json:"metadata"`
	ActorID        int64             `json:"actor_id"`
	Source         string            `json:"source"`
	ReversalOf     int64             `json:"reversal_of"`
	EventID        int64             `json:"event_id"`
	// RequireNonNegative lists balances this journal must not leave below
	// zero (on each classification's normal side), checked after the
	// entries are written, under the balance locks the post already holds.
	// A breach fails the post with *NegativeBalanceError. Not stored: an
	// idempotent replay moves no money, so it is not re-checked.
	RequireNonNegative []BalanceKey `json:"require_non_negative,omitempty"`
}

func (j *JournalInput) Totals() (debit, credit decimal.Decimal) {
	debit = decimal.Zero
	credit = decimal.Zero
	for _, e := range j.Entries {
		switch e.EntryType {
		case EntryTypeDebit:
			debit = debit.Add(e.Amount)
		case EntryTypeCredit:
			credit = credit.Add(e.Amount)
		}
	}
	return debit, credit
}

type currencyTotals struct {
	debit  decimal.Decimal
	credit decimal.Decimal
}

func (j *JournalInput) totalsByCurrency() map[int64]currencyTotals {
	totals := make(map[int64]currencyTotals)
	for _, e := range j.Entries {
		current := totals[e.CurrencyID]
		switch e.EntryType {
		case EntryTypeDebit:
			current.debit = current.debit.Add(e.Amount)
		case EntryTypeCredit:
			current.credit = current.credit.Add(e.Amount)
		}
		totals[e.CurrencyID] = current
	}
	return totals
}

func (j *JournalInput) Validate() error {
	if j.IdempotencyKey == "" {
		return fmt.Errorf("core: journal: idempotency key required: %w", ErrInvalidInput)
	}
	if len(j.Entries) == 0 {
		return fmt.Errorf("core: journal: entries must not be empty: %w", ErrInvalidInput)
	}
	for i, e := range j.Entries {
		// Magnitude first: adding decimals with different exponents
		// rescales them, which is where a pathological amount costs.
		if err := ValidateAmountMagnitude(e.Amount); err != nil {
			return fmt.Errorf("core: journal: entry[%d]: %w", i, err)
		}
		if e.AccountHolder == 0 {
			return fmt.Errorf("core: journal: entry[%d]: account_holder must not be zero: %w", i, ErrInvalidInput)
		}
		if !e.EntryType.IsValid() {
			return fmt.Errorf("core: journal: entry[%d]: invalid entry type %q: %w", i, e.EntryType, ErrInvalidInput)
		}
		if !e.Amount.IsPositive() {
			return fmt.Errorf("core: journal: entry[%d]: amount must be positive: %w", i, ErrInvalidInput)
		}
	}
	for i, k := range j.RequireNonNegative {
		if k.AccountHolder == 0 || k.CurrencyID <= 0 || k.ClassificationID <= 0 {
			return fmt.Errorf("core: journal: require_non_negative[%d]: holder, currency and classification are required: %w", i, ErrInvalidInput)
		}
	}
	totalsByCurrency := j.totalsByCurrency()
	currencyIDs := make([]int64, 0, len(totalsByCurrency))
	for currencyID := range totalsByCurrency {
		currencyIDs = append(currencyIDs, currencyID)
	}
	sort.Slice(currencyIDs, func(i, k int) bool { return currencyIDs[i] < currencyIDs[k] })

	for _, currencyID := range currencyIDs {
		totals := totalsByCurrency[currencyID]
		if !totals.debit.Equal(totals.credit) {
			return fmt.Errorf(
				"core: journal: currency %d unbalanced — debit=%s credit=%s: %w",
				currencyID,
				totals.debit,
				totals.credit,
				ErrUnbalancedJournal,
			)
		}
	}
	return nil
}

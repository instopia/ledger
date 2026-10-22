package core_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/instopia/ledger/core"
)

func TestValidateAmountMagnitude(t *testing.T) {
	ok := []string{"0", "1", "0.01", "999999999999", "999999999999.999999999999999999", "-999999999999",
		"1.000000000000000000000000", "0.000000000000000001"}
	bad := []string{"1000000000000", "0.0000000000000000001", "1E999999999", "1E-999999999", "-1E13", "123456789012345.5", "0E999999999", "0E-999999999", "-0E999999999"}
	for _, s := range ok {
		if err := core.ValidateAmountMagnitude(decimal.RequireFromString(s)); err != nil {
			t.Errorf("%s refused: %v", s, err)
		}
	}
	for _, s := range bad {
		start := time.Now()
		err := core.ValidateAmountMagnitude(decimal.RequireFromString(s))
		if !errors.Is(err, core.ErrInvalidInput) {
			t.Errorf("%s accepted", s)
		}
		// The bound must be decided without expanding the value.
		if el := time.Since(start); el > 100*time.Millisecond {
			t.Errorf("%s took %s", s, el)
		}
	}
}

func TestJournalValidateRejectsUnstorableAmountQuickly(t *testing.T) {
	in := core.JournalInput{IdempotencyKey: "k", Entries: []core.EntryInput{
		{AccountHolder: 1, CurrencyID: 1, ClassificationID: 1, EntryType: core.EntryTypeDebit, Amount: decimal.RequireFromString("1E999999999")},
		{AccountHolder: 2, CurrencyID: 1, ClassificationID: 1, EntryType: core.EntryTypeCredit, Amount: decimal.RequireFromString("1")},
	}}
	start := time.Now()
	if err := in.Validate(); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("validate = %v", err)
	}
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Fatalf("took %s", el)
	}
}

func TestJournalValidateRequireNonNegativeKeys(t *testing.T) {
	in := core.JournalInput{IdempotencyKey: "k", Entries: []core.EntryInput{
		{AccountHolder: 1, CurrencyID: 1, ClassificationID: 1, EntryType: core.EntryTypeDebit, Amount: decimal.NewFromInt(1)},
		{AccountHolder: 2, CurrencyID: 1, ClassificationID: 1, EntryType: core.EntryTypeCredit, Amount: decimal.NewFromInt(1)},
	}, RequireNonNegative: []core.BalanceKey{{AccountHolder: 1, CurrencyID: 1, ClassificationID: 1}}}
	if err := in.Validate(); err != nil {
		t.Fatalf("valid input refused: %v", err)
	}
	in.RequireNonNegative[0].ClassificationID = 0
	if err := in.Validate(); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("missing classification accepted: %v", err)
	}
}

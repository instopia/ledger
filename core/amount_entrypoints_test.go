package core_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/instopia/ledger/core"
)

// Every caller-supplied amount is bounded by ValidateAmountMagnitude before
// it is used in arithmetic or rendered. (decimal.Decimal cannot hold NaN or
// Inf; "NaN"/"Inf" don't even parse, and the schema refuses a NaN that
// reaches it by SQL. What can reach Validate is an amount too large or too
// fine for NUMERIC(30,18), which costs unbounded CPU to render.)
func TestEveryAmountEntryPointRejectsAnUnstorableAmount(t *testing.T) {
	for _, s := range []string{"NaN", "Inf", "+Inf", "-Inf", "Infinity"} {
		if _, err := decimal.NewFromString(s); err == nil {
			t.Errorf("%q parsed as a decimal", s)
		}
	}
	bad := []string{"1E999999999", "1E-999999999", "0E999999999", "0E-999999999", "1000000000000", "0.0000000000000000001"}
	type entry struct {
		name string
		run  func(a decimal.Decimal) error
	}
	entries := []entry{
		{"JournalInput", func(a decimal.Decimal) error {
			in := core.JournalInput{IdempotencyKey: "k", Entries: []core.EntryInput{
				{AccountHolder: 1, CurrencyID: 1, ClassificationID: 1, EntryType: core.EntryTypeDebit, Amount: a},
				{AccountHolder: 2, CurrencyID: 1, ClassificationID: 1, EntryType: core.EntryTypeCredit, Amount: a},
			}}
			return in.Validate()
		}},
		{"ReserveInput", func(a decimal.Decimal) error {
			return core.ReserveInput{AccountHolder: 1, CurrencyID: 1, Amount: a, IdempotencyKey: "k"}.Validate()
		}},
		{"AddPendingInput", func(a decimal.Decimal) error {
			return core.AddPendingInput{AccountHolder: 1, CurrencyID: 1, Amount: a, IdempotencyKey: "k"}.Validate()
		}},
		{"ConfirmPendingInput", func(a decimal.Decimal) error {
			return core.ConfirmPendingInput{AccountHolder: 1, CurrencyID: 1, Amount: a, IdempotencyKey: "k"}.Validate()
		}},
		{"CancelPendingInput", func(a decimal.Decimal) error {
			return core.CancelPendingInput{AccountHolder: 1, CurrencyID: 1, Amount: a, IdempotencyKey: "k"}.Validate()
		}},
		{"CreateBookingInput", func(a decimal.Decimal) error {
			return core.CreateBookingInput{ClassificationCode: "c", AccountHolder: 1, CurrencyID: 1, Amount: a, IdempotencyKey: "k"}.Validate()
		}},
		{"TransitionInput", func(a decimal.Decimal) error {
			return core.TransitionInput{BookingID: 1, ToStatus: "x", Amount: a}.Validate()
		}},
		{"EntryTemplate.Render", func(a decimal.Decimal) error {
			tmpl := core.EntryTemplate{Code: "t", Name: "T", JournalTypeID: 1, IsActive: true, Lines: []core.EntryTemplateLine{
				{ClassificationID: 1, EntryType: core.EntryTypeDebit, HolderRole: core.HolderRoleUser, AmountKey: "amount"},
				{ClassificationID: 2, EntryType: core.EntryTypeCredit, HolderRole: core.HolderRoleSystem, AmountKey: "amount"},
			}}
			_, err := tmpl.Render(core.TemplateParams{HolderID: 1, CurrencyID: 1, IdempotencyKey: "k", Amounts: map[string]decimal.Decimal{"amount": a}})
			return err
		}},
	}
	for _, e := range entries {
		if err := e.run(decimal.NewFromInt(5)); err != nil {
			t.Errorf("%s refused a normal amount: %v", e.name, err)
		}
		for _, s := range bad {
			start := time.Now()
			err := e.run(decimal.RequireFromString(s))
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Errorf("%s accepted %s: %v", e.name, s, err)
			}
			if el := time.Since(start); el > 100*time.Millisecond {
				t.Errorf("%s took %s on %s", e.name, el, s)
			}
		}
	}
}

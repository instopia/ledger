package ledger_test

// The library surface a consumer needs so it never reaches into the schema
// or re-implements engine protocol: LockBalances, WithTx, the Reader
// (JournalByKey, JournalEntries, SubBalances, HolderEntries, TrialBalance,
// ReversalChainViolations), ReverseJournalWith and
// JournalInput.RequireNonNegative; plus migration 022's guards (NaN amounts,
// INSERT path, TRUNCATE, reversal chains). One container, many subtests.

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/instopia/ledger"
	"github.com/instopia/ledger/core"
	"github.com/instopia/ledger/internal/postgrestest"
	"github.com/instopia/ledger/postgres"
)

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

func TestGroundwork(t *testing.T) {
	pool := postgrestest.SetupDB(t)
	ctx := context.Background()
	svc, err := ledger.New(pool)
	require.NoError(t, err)

	cur := postgrestest.SeedCurrency(t, pool, "AAA", "Test currency")
	jt := postgrestest.SeedJournalType(t, pool, "gw", "Groundwork")
	asset := postgrestest.SeedClassification(t, pool, "gw_asset", "Asset", "debit", false)
	liab := postgrestest.SeedClassification(t, pool, "gw_liability", "Liability", "credit", false)

	d := decimal.RequireFromString
	// pay: Dr asset(holder) / Cr liab(other).
	pay := func(key string, holder, other int64, amt string) core.JournalInput {
		return core.JournalInput{
			JournalTypeID: jt, IdempotencyKey: key, Source: "test",
			Entries: []core.EntryInput{
				{AccountHolder: holder, CurrencyID: cur, ClassificationID: asset, EntryType: core.EntryTypeDebit, Amount: d(amt)},
				{AccountHolder: other, CurrencyID: cur, ClassificationID: liab, EntryType: core.EntryTypeCredit, Amount: d(amt)},
			},
		}
	}
	post := func(t *testing.T, in core.JournalInput) *core.Journal {
		t.Helper()
		j, err := svc.JournalWriter().PostJournal(ctx, in)
		require.NoError(t, err)
		return j
	}

	t.Run("LockBalances is the posting lock and needs a transaction", func(t *testing.T) {
		err := svc.LockBalances(ctx, cur, 7001)
		require.ErrorIs(t, err, core.ErrOutsideTransaction)

		held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- svc.RunInTx(ctx, func(tx *ledger.Service) error {
				if err := tx.LockBalances(ctx, cur, 7001, 7002, 7001); err != nil {
					return err
				}
				close(held)
				<-release
				return nil
			})
		}()
		<-held
		probe := svc.RunInTx(ctx, func(tx *ledger.Service) error {
			if _, err := tx.DBTX().Exec(ctx, `SET LOCAL lock_timeout = '300ms'`); err != nil {
				return err
			}
			_, err := tx.JournalWriter().PostJournal(ctx, pay("lock-probe", 7002, 7003, "1"))
			return err
		})
		close(release)
		require.NoError(t, <-done)
		assert.Equal(t, "55P03", pgCode(probe), "a post on a locked holder must wait: %v", probe)
	})

	t.Run("WithTx joins the caller's transaction", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		_, err = svc.WithTx(tx).JournalWriter().PostJournal(ctx, pay("withtx-rolled-back", 7101, 7102, "5"))
		require.NoError(t, err)
		_, err = svc.WithTx(tx).Reader().JournalByKey(ctx, "withtx-rolled-back")
		require.NoError(t, err, "the clone's reader sees its transaction's writes")
		require.NoError(t, tx.Rollback(ctx))
		_, err = svc.Reader().JournalByKey(ctx, "withtx-rolled-back")
		require.ErrorIs(t, err, core.ErrNotFound)

		tx, err = pool.Begin(ctx)
		require.NoError(t, err)
		_, err = svc.WithTx(tx).JournalWriter().PostJournal(ctx, pay("withtx-committed", 7101, 7102, "5"))
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))
		j, err := svc.Reader().JournalByKey(ctx, "withtx-committed")
		require.NoError(t, err)
		assert.Equal(t, "withtx-committed", j.IdempotencyKey)
	})

	t.Run("ReverseJournalWith mirrors under the caller's key, once", func(t *testing.T) {
		orig := post(t, core.JournalInput{
			JournalTypeID: jt, IdempotencyKey: "rev-orig", Source: "test",
			Entries: []core.EntryInput{
				{AccountHolder: 7201, CurrencyID: cur, ClassificationID: asset, EntryType: core.EntryTypeDebit, Amount: d("10.25")},
				{AccountHolder: 7201, CurrencyID: cur, ClassificationID: asset, EntryType: core.EntryTypeDebit, Amount: d("4")},
				{AccountHolder: 7202, CurrencyID: cur, ClassificationID: liab, EntryType: core.EntryTypeCredit, Amount: d("14.25")},
			},
		})
		in := core.ReverseInput{JournalID: orig.ID, IdempotencyKey: "reversal:rev-orig", Source: "void", ActorID: 42}

		// Inside a caller's transaction, rolled back: nothing remains.
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		_, err = svc.WithTx(tx).JournalReverser().ReverseJournalWith(ctx, in)
		require.NoError(t, err)
		require.NoError(t, tx.Rollback(ctx))
		_, err = svc.Reader().JournalByKey(ctx, "reversal:rev-orig")
		require.ErrorIs(t, err, core.ErrNotFound)

		var rev *core.Journal
		require.NoError(t, svc.RunInTx(ctx, func(tx *ledger.Service) error {
			var err error
			rev, err = tx.JournalReverser().ReverseJournalWith(ctx, in)
			return err
		}))
		assert.Equal(t, "reversal:rev-orig", rev.IdempotencyKey)
		assert.Equal(t, orig.ID, rev.ReversalOf)
		assert.Equal(t, "void", rev.Source)
		assert.Equal(t, int64(42), rev.ActorID)

		oe, err := svc.Reader().JournalEntries(ctx, orig.ID)
		require.NoError(t, err)
		re, err := svc.Reader().JournalEntries(ctx, rev.ID)
		require.NoError(t, err)
		require.Len(t, re, len(oe))
		for i := range oe {
			assert.Equal(t, oe[i].AccountHolder, re[i].AccountHolder)
			assert.Equal(t, oe[i].ClassificationID, re[i].ClassificationID)
			assert.True(t, oe[i].Amount.Equal(re[i].Amount))
			assert.NotEqual(t, oe[i].EntryType, re[i].EntryType)
		}

		again, err := svc.JournalReverser().ReverseJournalWith(ctx, in)
		require.NoError(t, err, "same key: an idempotent retry")
		assert.Equal(t, rev.ID, again.ID)

		_, err = svc.JournalReverser().ReverseJournalWith(ctx, core.ReverseInput{JournalID: orig.ID, IdempotencyKey: "another"})
		require.ErrorIs(t, err, core.ErrConflict, "a journal is reversed at most once")
		_, err = svc.JournalReverser().ReverseJournalWith(ctx, core.ReverseInput{JournalID: rev.ID})
		require.ErrorIs(t, err, core.ErrConflict, "a reversal is never itself reversed")

		v, err := svc.Reader().ReversalChainViolations(ctx)
		require.NoError(t, err)
		assert.Empty(t, v)
	})

	t.Run("reversal chain integrity: the schema refuses chains, the scan finds non-mirrors", func(t *testing.T) {
		a := post(t, pay("chain-a", 7301, 7302, "3"))
		b := post(t, pay("chain-b", 7301, 7302, "3"))
		// A "reversal" of a written straight to the table: same shape as the
		// original, not flipped (a net-doubling forgery).
		require.NoError(t, svc.RunInTx(ctx, func(tx *ledger.Service) error {
			var id int64
			if err := tx.DBTX().QueryRow(ctx, `INSERT INTO journals (journal_type_id, idempotency_key, total_debit, total_credit, source, reversal_of)
				VALUES ($1, 'forged', 3, 3, 'x', $2) RETURNING id`, jt, a.ID).Scan(&id); err != nil {
				return err
			}
			_, err := tx.DBTX().Exec(ctx, `INSERT INTO journal_entries (journal_id, account_holder, currency_id, classification_id, entry_type, amount)
				VALUES ($1, 7301, $2, $3, 'debit', 3), ($1, 7302, $2, $4, 'credit', 3)`, id, cur, asset, liab)
			return err
		}))
		v, err := svc.Reader().ReversalChainViolations(ctx)
		require.NoError(t, err)
		forged, err := svc.Reader().JournalByKey(ctx, "forged")
		require.NoError(t, err)
		assert.Contains(t, v, core.ReversalViolation{Kind: core.ReversalNotMirror, ReversalJournalID: forged.ID, OriginalJournalID: a.ID})

		// Reversing a reversal is refused by the schema too.
		err = svc.RunInTx(ctx, func(tx *ledger.Service) error {
			_, err := tx.DBTX().Exec(ctx, `INSERT INTO journals (journal_type_id, idempotency_key, total_debit, total_credit, source, reversal_of)
				VALUES ($1, 'chain', 3, 3, 'x', $2)`, jt, forged.ID)
			return err
		})
		assert.Equal(t, "23514", pgCode(err), "reversal of a reversal: %v", err)
		_ = b
	})

	t.Run("reads: normal-side sub-balances, holder entries, trial balance, as of", func(t *testing.T) {
		cur2 := postgrestest.SeedCurrency(t, pool, "BBB", "Read currency")
		in := func(key string, amt string) core.JournalInput {
			j := pay(key, 7401, 7402, amt)
			for i := range j.Entries {
				j.Entries[i].CurrencyID = cur2
			}
			return j
		}
		j1 := post(t, in("read-1", "100"))
		time.Sleep(5 * time.Millisecond) // distinct transaction times
		j2 := post(t, in("read-2", "30.50"))

		subs, err := svc.Reader().SubBalances(ctx, core.BalanceFilter{CurrencyID: cur2})
		require.NoError(t, err)
		require.Len(t, subs, 2)
		for _, s := range subs {
			switch s.ClassificationID {
			case asset:
				assert.Equal(t, int64(7401), s.AccountHolder)
				assert.Equal(t, core.NormalSideDebit, s.NormalSide)
				assert.True(t, s.Balance.Equal(d("130.5")), "asset %s", s.Balance)
				assert.True(t, s.Debits.Equal(d("130.5")))
			case liab:
				assert.Equal(t, core.NormalSideCredit, s.NormalSide)
				assert.True(t, s.Balance.Equal(d("130.5")), "a credit-normal balance is positive: %s", s.Balance)
				assert.True(t, s.Credits.Equal(d("130.5")))
			default:
				t.Errorf("unexpected classification %d", s.ClassificationID)
			}
		}
		// As of j2's time: j1 only (strictly before).
		asOf, err := svc.Reader().SubBalances(ctx, core.BalanceFilter{CurrencyID: cur2, Before: j2.CreatedAt, ClassificationIDs: []int64{liab}})
		require.NoError(t, err)
		require.Len(t, asOf, 1)
		assert.True(t, asOf[0].Balance.Equal(d("100")), "as of: %s", asOf[0].Balance)
		none, err := svc.Reader().SubBalances(ctx, core.BalanceFilter{CurrencyID: cur2, Before: j1.CreatedAt})
		require.NoError(t, err)
		assert.Empty(t, none)

		he, err := svc.Reader().HolderEntries(ctx, 7401, core.BalanceFilter{CurrencyID: cur2})
		require.NoError(t, err)
		require.Len(t, he, 2)
		assert.Equal(t, "read-1", he[0].IdempotencyKey)
		assert.Equal(t, "read-2", he[1].IdempotencyKey)
		assert.Equal(t, core.EntryTypeDebit, he[1].EntryType)
		assert.True(t, he[1].Amount.Equal(d("30.5")))

		tb, err := svc.Reader().TrialBalance(ctx, core.BalanceFilter{CurrencyID: cur2})
		require.NoError(t, err)
		require.Len(t, tb, 2)
		dr, cr := decimal.Zero, decimal.Zero
		for _, l := range tb {
			dr, cr = dr.Add(l.Debits), cr.Add(l.Credits)
			assert.True(t, l.Balance.Equal(d("130.5")), "%s %s", l.Code, l.Balance)
		}
		assert.True(t, dr.Equal(cr), "a trial balance balances")

		_, err = svc.Reader().SubBalances(ctx, core.BalanceFilter{})
		require.ErrorIs(t, err, core.ErrInvalidInput)
	})

	t.Run("RequireNonNegative refuses a post that would overdraw", func(t *testing.T) {
		post(t, pay("nn-fund", 7501, 7502, "50"))
		// Dr liab(7502) 60 / Cr asset(7501) 60 would leave both at -10.
		over := core.JournalInput{
			JournalTypeID: jt, IdempotencyKey: "nn-over", Source: "test",
			Entries: []core.EntryInput{
				{AccountHolder: 7502, CurrencyID: cur, ClassificationID: liab, EntryType: core.EntryTypeDebit, Amount: d("60")},
				{AccountHolder: 7501, CurrencyID: cur, ClassificationID: asset, EntryType: core.EntryTypeCredit, Amount: d("60")},
			},
			RequireNonNegative: []core.BalanceKey{{AccountHolder: 7501, CurrencyID: cur, ClassificationID: asset}},
		}
		_, err := svc.JournalWriter().PostJournal(ctx, over)
		require.ErrorIs(t, err, core.ErrInsufficientBalance)
		var nb *core.NegativeBalanceError
		require.ErrorAs(t, err, &nb)
		assert.True(t, nb.Balance.Equal(d("-10")), "balance %s", nb.Balance)
		assert.True(t, nb.Change.Equal(d("-60")), "change %s", nb.Change)
		assert.True(t, nb.Balance.Sub(nb.Change).Equal(d("50")), "held before")
		_, err = svc.Reader().JournalByKey(ctx, "nn-over")
		require.ErrorIs(t, err, core.ErrNotFound, "nothing posted")

		// Exactly to zero is allowed; so is a guard on a credit-normal key.
		exact := over
		exact.IdempotencyKey = "nn-exact"
		exact.Entries = []core.EntryInput{over.Entries[0], over.Entries[1]}
		exact.Entries[0].Amount, exact.Entries[1].Amount = d("50"), d("50")
		exact.RequireNonNegative = append(exact.RequireNonNegative, core.BalanceKey{AccountHolder: 7502, CurrencyID: cur, ClassificationID: liab})
		post(t, exact)

		// A guarded key the journal doesn't touch is still checked.
		other := pay("nn-untouched", 7503, 7504, "1")
		other.RequireNonNegative = []core.BalanceKey{{AccountHolder: 7999, CurrencyID: cur, ClassificationID: asset}}
		post(t, other)

		bad := pay("nn-bad", 7503, 7504, "1")
		bad.RequireNonNegative = []core.BalanceKey{{AccountHolder: 0, CurrencyID: cur, ClassificationID: asset}}
		_, err = svc.JournalWriter().PostJournal(ctx, bad)
		require.ErrorIs(t, err, core.ErrInvalidInput)
	})

	t.Run("NaN and unstorable amounts are refused", func(t *testing.T) {
		_, err := svc.JournalWriter().PostJournal(ctx, pay("nan-huge", 7601, 7602, "1E999999999"))
		require.ErrorIs(t, err, core.ErrInvalidInput)

		j := post(t, pay("nan-host", 7601, 7602, "1"))
		err = svc.RunInTx(ctx, func(tx *ledger.Service) error {
			// Inside the posting transaction's own time would pass the
			// INSERT guard; the CHECK must still refuse NaN.
			var id int64
			if err := tx.DBTX().QueryRow(ctx, `INSERT INTO journals (journal_type_id, idempotency_key, total_debit, total_credit, source)
				VALUES ($1, 'nan', 1, 1, 'x') RETURNING id`, jt).Scan(&id); err != nil {
				return err
			}
			_, err := tx.DBTX().Exec(ctx, `INSERT INTO journal_entries (journal_id, account_holder, currency_id, classification_id, entry_type, amount)
				VALUES ($1, 7601, $2, $3, 'debit', 'NaN')`, id, cur, asset)
			return err
		})
		assert.Equal(t, "23514", pgCode(err), "NaN entry: %v", err)
		err = svc.RunInTx(ctx, func(tx *ledger.Service) error {
			_, err := tx.DBTX().Exec(ctx, `INSERT INTO journals (journal_type_id, idempotency_key, total_debit, total_credit, source)
				VALUES ($1, 'nan2', 'NaN', 'NaN', 'x')`, jt)
			return err
		})
		assert.Equal(t, "23514", pgCode(err), "NaN totals: %v", err)
		_ = j
	})

	t.Run("INSERT guards: no appended entries, no back-dating", func(t *testing.T) {
		j := post(t, pay("ins-host", 7701, 7702, "2"))
		_, err := pool.Exec(ctx, `INSERT INTO journal_entries (journal_id, account_holder, currency_id, classification_id, entry_type, amount)
			VALUES ($1, 7701, $2, $3, 'debit', 1)`, j.ID, cur, asset)
		assert.Equal(t, "23514", pgCode(err), "an entry appended to a posted journal: %v", err)

		_, err = pool.Exec(ctx, `INSERT INTO journals (journal_type_id, idempotency_key, total_debit, total_credit, source, created_at)
			VALUES ($1, 'backdated', 1, 1, 'x', now() - interval '1 day')`, jt)
		assert.Equal(t, "23514", pgCode(err), "a back-dated journal: %v", err)

		err = svc.RunInTx(ctx, func(tx *ledger.Service) error {
			var id int64
			if err := tx.DBTX().QueryRow(ctx, `INSERT INTO journals (journal_type_id, idempotency_key, total_debit, total_credit, source)
				VALUES ($1, 'backdated-entry', 1, 1, 'x') RETURNING id`, jt).Scan(&id); err != nil {
				return err
			}
			_, err := tx.DBTX().Exec(ctx, `INSERT INTO journal_entries (journal_id, account_holder, currency_id, classification_id, entry_type, amount, created_at)
				VALUES ($1, 7701, $2, $3, 'debit', 1, now() - interval '1 day')`, id, cur, asset)
			return err
		})
		assert.Equal(t, "23514", pgCode(err), "a back-dated entry: %v", err)
	})

	t.Run("TRUNCATE needs the deliberate opt-in", func(t *testing.T) {
		for _, table := range []string{"journals", "journal_entries", "journal_entries_default"} {
			_, err := pool.Exec(ctx, `TRUNCATE `+table+` CASCADE`)
			assert.Equal(t, "23514", pgCode(err), "TRUNCATE %s: %v", table, err)
		}
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `SET LOCAL ledger.allow_truncate = 'on'`)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `TRUNCATE journal_entries, journals CASCADE`)
		require.NoError(t, err)
		require.NoError(t, tx.Rollback(ctx))
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM journals`).Scan(&n))
		assert.Positive(t, n, "rolled back")
	})
}

// Migrations exposes the embedded schema files byte-for-byte, so a consumer
// that provisions the schema inside its own migration sequence can prove
// its copy is identical.
func TestMigrationsFS(t *testing.T) {
	b, err := fs.ReadFile(postgres.Migrations(), "022_integrity_guards.up.sql")
	require.NoError(t, err)
	assert.Contains(t, string(b), "ledger.allow_truncate")
	entries, err := fs.ReadDir(postgres.Migrations(), ".")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 43)
}

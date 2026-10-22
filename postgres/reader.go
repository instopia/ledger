package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/instopia/ledger/core"
	"github.com/instopia/ledger/postgres/sqlcgen"
)

// Querier is the read-only executor a Reader needs: a *pgxpool.Pool, a
// pgx.Tx, or a single connection all satisfy it.
type Querier = sqlcgen.DBTX

// Compile-time interface assertion.
var _ core.LedgerReader = (*Reader)(nil)

// Reader implements core.LedgerReader over any executor. Bound to a
// transaction it sees that transaction's own writes.
//
// The aggregate queries are hand-written rather than sqlc-generated: they
// take optional filters (classification set, as-of instant) and return
// sums as text, so they never depend on a NUMERIC codec being registered.
type Reader struct {
	db Querier
	q  *sqlcgen.Queries
}

// NewReader returns a Reader over db.
func NewReader(db Querier) *Reader {
	return &Reader{db: db, q: sqlcgen.New(db)}
}

// JournalByKey implements core.LedgerReader.
func (r *Reader) JournalByKey(ctx context.Context, key string) (*core.Journal, error) {
	row, err := r.q.GetJournalByIdempotencyKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres: journal by key %q: %w", key, core.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: journal by key: %w", err)
	}
	return journalFromRow(row), nil
}

// JournalEntries implements core.LedgerReader.
func (r *Reader) JournalEntries(ctx context.Context, journalID int64) ([]core.Entry, error) {
	rows, err := r.q.ListJournalEntries(ctx, journalID)
	if err != nil {
		return nil, fmt.Errorf("postgres: journal entries: %w", err)
	}
	out := make([]core.Entry, len(rows))
	for i, row := range rows {
		out[i] = *entryFromRow(row)
	}
	return out, nil
}

// beforeArg maps the zero time to NULL (no as-of bound).
func beforeArg(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// classArg maps an empty classification set to NULL (every classification).
func classArg(ids []int64) any {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func parseDec(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("postgres: parse amount %q: %w", s, err)
	}
	return d, nil
}

// normal returns the normal-side balance from gross debits and credits.
func normal(side core.NormalSide, debits, credits decimal.Decimal) decimal.Decimal {
	if side == core.NormalSideCredit {
		return credits.Sub(debits)
	}
	return debits.Sub(credits)
}

// SubBalances implements core.LedgerReader.
func (r *Reader) SubBalances(ctx context.Context, f core.BalanceFilter) ([]core.SubBalance, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx,
		`SELECT e.account_holder, e.classification_id, c.normal_side,
		        COALESCE(SUM(e.amount) FILTER (WHERE e.entry_type = 'debit'), 0)::text,
		        COALESCE(SUM(e.amount) FILTER (WHERE e.entry_type = 'credit'), 0)::text
		   FROM journal_entries e
		   JOIN classifications c ON c.id = e.classification_id
		  WHERE e.currency_id = $1
		    AND ($2::bigint[] IS NULL OR e.classification_id = ANY($2))
		    AND ($3::timestamptz IS NULL OR e.created_at < $3)
		  GROUP BY e.account_holder, e.classification_id, c.normal_side
		  ORDER BY e.classification_id, e.account_holder`,
		f.CurrencyID, classArg(f.ClassificationIDs), beforeArg(f.Before))
	if err != nil {
		return nil, fmt.Errorf("postgres: sub-balances: %w", err)
	}
	defer rows.Close()
	var out []core.SubBalance
	for rows.Next() {
		var sb core.SubBalance
		var side, dr, cr string
		if err := rows.Scan(&sb.AccountHolder, &sb.ClassificationID, &side, &dr, &cr); err != nil {
			return nil, fmt.Errorf("postgres: sub-balances: scan: %w", err)
		}
		sb.CurrencyID = f.CurrencyID
		sb.NormalSide = core.NormalSide(side)
		if sb.Debits, err = parseDec(dr); err != nil {
			return nil, err
		}
		if sb.Credits, err = parseDec(cr); err != nil {
			return nil, err
		}
		sb.Balance = normal(sb.NormalSide, sb.Debits, sb.Credits)
		out = append(out, sb)
	}
	return out, rows.Err()
}

// HolderEntries implements core.LedgerReader.
func (r *Reader) HolderEntries(ctx context.Context, holder int64, f core.BalanceFilter) ([]core.HolderEntry, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx,
		`SELECT e.id, e.journal_id, j.idempotency_key, e.classification_id, e.entry_type, e.amount::text, e.created_at
		   FROM journal_entries e
		   JOIN journals j ON j.id = e.journal_id
		  WHERE e.account_holder = $1 AND e.currency_id = $2
		    AND ($3::bigint[] IS NULL OR e.classification_id = ANY($3))
		    AND ($4::timestamptz IS NULL OR e.created_at < $4)
		  ORDER BY e.created_at, e.id`,
		holder, f.CurrencyID, classArg(f.ClassificationIDs), beforeArg(f.Before))
	if err != nil {
		return nil, fmt.Errorf("postgres: holder entries: %w", err)
	}
	defer rows.Close()
	var out []core.HolderEntry
	for rows.Next() {
		var he core.HolderEntry
		var side, amt string
		if err := rows.Scan(&he.EntryID, &he.JournalID, &he.IdempotencyKey, &he.ClassificationID, &side, &amt, &he.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: holder entries: scan: %w", err)
		}
		he.CurrencyID = f.CurrencyID
		he.EntryType = core.EntryType(side)
		if he.Amount, err = parseDec(amt); err != nil {
			return nil, err
		}
		out = append(out, he)
	}
	return out, rows.Err()
}

// TrialBalance implements core.LedgerReader.
func (r *Reader) TrialBalance(ctx context.Context, f core.BalanceFilter) ([]core.TrialBalanceLine, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx,
		`SELECT e.classification_id, c.code, c.normal_side,
		        COALESCE(SUM(e.amount) FILTER (WHERE e.entry_type = 'debit'), 0)::text,
		        COALESCE(SUM(e.amount) FILTER (WHERE e.entry_type = 'credit'), 0)::text
		   FROM journal_entries e
		   JOIN classifications c ON c.id = e.classification_id
		  WHERE e.currency_id = $1
		    AND ($2::bigint[] IS NULL OR e.classification_id = ANY($2))
		    AND ($3::timestamptz IS NULL OR e.created_at < $3)
		  GROUP BY e.classification_id, c.code, c.normal_side
		  ORDER BY c.code`,
		f.CurrencyID, classArg(f.ClassificationIDs), beforeArg(f.Before))
	if err != nil {
		return nil, fmt.Errorf("postgres: trial balance: %w", err)
	}
	defer rows.Close()
	var out []core.TrialBalanceLine
	for rows.Next() {
		var tl core.TrialBalanceLine
		var side, dr, cr string
		if err := rows.Scan(&tl.ClassificationID, &tl.Code, &side, &dr, &cr); err != nil {
			return nil, fmt.Errorf("postgres: trial balance: scan: %w", err)
		}
		tl.NormalSide = core.NormalSide(side)
		if tl.Debits, err = parseDec(dr); err != nil {
			return nil, err
		}
		if tl.Credits, err = parseDec(cr); err != nil {
			return nil, err
		}
		tl.Balance = normal(tl.NormalSide, tl.Debits, tl.Credits)
		out = append(out, tl)
	}
	return out, rows.Err()
}

// ReversalChainViolations implements core.LedgerReader.
//
// A reversal R of original O is a mirror when, for every dimension
// (holder, currency, classification, side), R's total on the flipped side
// equals O's total, and R touches nothing O did not. The unique index on
// journals.reversal_of already makes "reversed at most once" structural;
// this scan is the detection half for rows that reached the table some other
// way than ReverseJournal(With).
func (r *Reader) ReversalChainViolations(ctx context.Context) ([]core.ReversalViolation, error) {
	rows, err := r.db.Query(ctx,
		`WITH links AS (
		     SELECT r.id AS reversal_id, r.reversal_of AS original_id
		       FROM journals r WHERE r.reversal_of IS NOT NULL
		 ),
		 orig AS (
		     SELECT l.reversal_id, e.account_holder, e.currency_id, e.classification_id, e.entry_type, SUM(e.amount) AS amount
		       FROM links l JOIN journal_entries e ON e.journal_id = l.original_id
		      GROUP BY 1, 2, 3, 4, 5
		 ),
		 flipped AS (
		     SELECT l.reversal_id, e.account_holder, e.currency_id, e.classification_id,
		            CASE WHEN e.entry_type = 'debit' THEN 'credit' ELSE 'debit' END AS entry_type, SUM(e.amount) AS amount
		       FROM links l JOIN journal_entries e ON e.journal_id = l.reversal_id
		      GROUP BY 1, 2, 3, 4, 5
		 )
		 SELECT 'reversal_of_reversal', l.reversal_id, l.original_id
		   FROM links l JOIN journals o ON o.id = l.original_id
		  WHERE o.reversal_of IS NOT NULL
		 UNION
		 SELECT 'reversal_not_mirror', l.reversal_id, l.original_id
		   FROM links l
		  WHERE EXISTS (
		      SELECT 1 FROM orig o FULL JOIN flipped f
		          ON f.reversal_id = o.reversal_id AND f.account_holder = o.account_holder AND f.currency_id = o.currency_id
		         AND f.classification_id = o.classification_id AND f.entry_type = o.entry_type
		       WHERE COALESCE(o.reversal_id, f.reversal_id) = l.reversal_id
		         AND (o.amount IS NULL OR f.amount IS NULL OR o.amount <> f.amount))
		 ORDER BY 2, 1`)
	if err != nil {
		return nil, fmt.Errorf("postgres: reversal chain integrity: %w", err)
	}
	defer rows.Close()
	var out []core.ReversalViolation
	for rows.Next() {
		var v core.ReversalViolation
		if err := rows.Scan(&v.Kind, &v.ReversalJournalID, &v.OriginalJournalID); err != nil {
			return nil, fmt.Errorf("postgres: reversal chain integrity: scan: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

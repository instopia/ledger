-- 022_integrity_guards: generic hardening of the append-only journal tables.
--
-- 1. NaN is not an amount. NUMERIC accepts 'NaN', and NaN sorts above every
--    finite value, so the existing CHECK (amount > 0) lets it through; it
--    then fails every read that converts it. `<col>::text <> 'NaN'` is the
--    formulation that works (NaN = NaN is true on numeric). Infinity is
--    already refused by the NUMERIC(30,18) typmod.
--
-- 2. INSERT-path guards. The UPDATE/DELETE triggers (004) make a posted
--    journal immutable, but nothing looked at an INSERT, so a posted journal
--    could still be changed by APPENDING entries to it later, and a journal
--    or entry could be stamped with any created_at (the as-of reads trust
--    it). Now:
--      - a journal is stamped with its transaction's time (now());
--      - an entry is stamped with its transaction's time, and only the
--        transaction that inserted its journal may add entries to it
--        (the journal's created_at is that same transaction time);
--      - a journal can't reverse a journal that is itself a reversal
--        (with uq_journals_reversal_of: a journal is reversed at most once,
--        and reversals never chain).
--
-- 3. TRUNCATE guard. TRUNCATE skips row triggers, so it was the one way to
--    erase posted journals wholesale. It is refused on journals and
--    journal_entries (and its default partition: statement triggers are not
--    inherited by partitions) unless the transaction deliberately opts in:
--        SET LOCAL ledger.allow_truncate = 'on'
--    (e.g. a test-data reset). Nothing in the engine sets it.

ALTER TABLE journal_entries
    ADD CONSTRAINT chk_journal_entries_amount_not_nan CHECK (amount::text <> 'NaN');
ALTER TABLE journals
    ADD CONSTRAINT chk_journals_totals_not_nan CHECK (total_debit::text <> 'NaN' AND total_credit::text <> 'NaN');

CREATE OR REPLACE FUNCTION ledger_journals_guard_insert() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.created_at IS DISTINCT FROM now() THEN
        RAISE EXCEPTION 'ledger: a journal is stamped with its transaction''s time (%), not %', now(), NEW.created_at
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.reversal_of IS NOT NULL
       AND EXISTS (SELECT 1 FROM public.journals o WHERE o.id = NEW.reversal_of AND o.reversal_of IS NOT NULL) THEN
        RAISE EXCEPTION 'ledger: journal % is itself a reversal and cannot be reversed', NEW.reversal_of
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER journals_guard_insert BEFORE INSERT ON journals
    FOR EACH ROW EXECUTE FUNCTION ledger_journals_guard_insert();

CREATE OR REPLACE FUNCTION ledger_journal_entries_guard_insert() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.created_at IS DISTINCT FROM now() THEN
        RAISE EXCEPTION 'ledger: a journal entry is stamped with its transaction''s time (%), not %', now(), NEW.created_at
            USING ERRCODE = 'check_violation';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.journals j WHERE j.id = NEW.journal_id AND j.created_at = now()) THEN
        RAISE EXCEPTION 'ledger: entries can only be added by the transaction that posts their journal (journal %)', NEW.journal_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER journal_entries_guard_insert BEFORE INSERT ON journal_entries
    FOR EACH ROW EXECUTE FUNCTION ledger_journal_entries_guard_insert();

CREATE OR REPLACE FUNCTION ledger_block_truncate() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF COALESCE(current_setting('ledger.allow_truncate', true), '') = 'on' THEN
        RETURN NULL;
    END IF;
    RAISE EXCEPTION 'ledger: TRUNCATE on % is not allowed (a deliberate reset sets ledger.allow_truncate = on for its transaction)', TG_TABLE_NAME
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER journals_no_truncate BEFORE TRUNCATE ON journals
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_block_truncate();
CREATE TRIGGER journal_entries_no_truncate BEFORE TRUNCATE ON journal_entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_block_truncate();
CREATE TRIGGER journal_entries_default_no_truncate BEFORE TRUNCATE ON journal_entries_default
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_block_truncate();

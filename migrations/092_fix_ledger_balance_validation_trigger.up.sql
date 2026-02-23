-- Fix ledger balance validation to support multi-entry transactions.
-- Previous implementation validated after each entry insert, which incorrectly
-- failed valid transactions with more than 2 entries.

-- Remove legacy per-entry validation trigger.
DROP TRIGGER IF EXISTS validate_ledger_entries_balance ON ledger_entries;
DROP FUNCTION IF EXISTS validate_ledger_balance();

-- Validate ledger balance only when transaction status moves to completed.
CREATE OR REPLACE FUNCTION validate_ledger_transaction_on_complete()
RETURNS TRIGGER AS $$
DECLARE
    debit_sum DECIMAL(36, 18);
    credit_sum DECIMAL(36, 18);
    entry_count INT;
BEGIN
    -- Only validate when transitioning into completed status.
    IF NEW.status <> 'completed' OR OLD.status = 'completed' THEN
        RETURN NEW;
    END IF;

    SELECT COUNT(*) INTO entry_count
    FROM ledger_entries
    WHERE transaction_id = NEW.id;

    IF entry_count < 2 THEN
        RAISE EXCEPTION 'Ledger transaction % must have at least 2 entries', NEW.id;
    END IF;

    SELECT COALESCE(SUM(amount), 0) INTO debit_sum
    FROM ledger_entries
    WHERE transaction_id = NEW.id
      AND entry_type = 'debit';

    SELECT COALESCE(SUM(amount), 0) INTO credit_sum
    FROM ledger_entries
    WHERE transaction_id = NEW.id
      AND entry_type = 'credit';

    IF debit_sum <> credit_sum THEN
        RAISE EXCEPTION 'Ledger transaction % is unbalanced: debits=%, credits=%',
            NEW.id, debit_sum, credit_sum;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS validate_ledger_transaction_balance_on_complete ON ledger_transactions;
CREATE TRIGGER validate_ledger_transaction_balance_on_complete
    BEFORE UPDATE OF status ON ledger_transactions
    FOR EACH ROW
    EXECUTE FUNCTION validate_ledger_transaction_on_complete();

-- Restore legacy per-entry ledger balance validation behavior.

DROP TRIGGER IF EXISTS validate_ledger_transaction_balance_on_complete ON ledger_transactions;
DROP FUNCTION IF EXISTS validate_ledger_transaction_on_complete();

CREATE OR REPLACE FUNCTION validate_ledger_balance()
RETURNS TRIGGER AS $$
DECLARE
    debit_sum DECIMAL(36, 18);
    credit_sum DECIMAL(36, 18);
    entry_count INT;
BEGIN
    SELECT COUNT(*) INTO entry_count
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id;

    IF entry_count < 2 THEN
        RETURN NEW;
    END IF;

    SELECT COALESCE(SUM(amount), 0) INTO debit_sum
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id
      AND entry_type = 'debit';

    SELECT COALESCE(SUM(amount), 0) INTO credit_sum
    FROM ledger_entries
    WHERE transaction_id = NEW.transaction_id
      AND entry_type = 'credit';

    IF debit_sum <> credit_sum THEN
        RAISE EXCEPTION 'Ledger transaction % is unbalanced: debits=%, credits=%',
            NEW.transaction_id, debit_sum, credit_sum;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS validate_ledger_entries_balance ON ledger_entries;
CREATE TRIGGER validate_ledger_entries_balance
    AFTER INSERT ON ledger_entries
    FOR EACH ROW
    EXECUTE FUNCTION validate_ledger_balance();

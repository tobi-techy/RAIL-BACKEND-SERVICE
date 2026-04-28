-- Replace BEFORE UPDATE trigger with a CONSTRAINT TRIGGER that is DEFERRABLE
-- INITIALLY DEFERRED, so validation fires at transaction commit instead of per-row.
-- This prevents intermediate validation failures for multi-entry (e.g. 4-entry) transactions.

DROP TRIGGER IF EXISTS validate_ledger_transaction_balance_on_complete ON ledger_transactions;

CREATE CONSTRAINT TRIGGER validate_ledger_transaction_balance_on_complete
    AFTER UPDATE OF status ON ledger_transactions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION validate_ledger_transaction_on_complete();

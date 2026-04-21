-- Revert to non-deferred BEFORE UPDATE trigger.

DROP TRIGGER IF EXISTS validate_ledger_transaction_balance_on_complete ON ledger_transactions;

CREATE TRIGGER validate_ledger_transaction_balance_on_complete
    BEFORE UPDATE OF status ON ledger_transactions
    FOR EACH ROW
    EXECUTE FUNCTION validate_ledger_transaction_on_complete();

package reconciliation

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

const tracerName = "reconciliation.checks"

// CheckLedgerConsistency verifies double-entry bookkeeping integrity
func (s *Service) CheckLedgerConsistency(ctx context.Context, reportID uuid.UUID) (*entities.ReconciliationCheckResult, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "CheckLedgerConsistency")
	defer span.End()

	startTime := time.Now()
	result := &entities.ReconciliationCheckResult{
		CheckType:  entities.ReconciliationCheckLedgerConsistency,
		Exceptions: []entities.ReconciliationException{},
		Metadata:   make(map[string]interface{}),
	}

	// 1. Check sum of debits = sum of credits
	totalDebits, totalCredits, err := s.ledgerRepo.GetTotalDebitsAndCredits(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get ledger totals: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.SetAttributes(attribute.Bool("passed", false))
		span.RecordError(err)
		return result, err
	}

	result.ExpectedValue = totalDebits
	result.ActualValue = totalCredits
	result.Difference = totalCredits.Sub(totalDebits)

	if !result.Difference.IsZero() {
		severity := entities.DetermineSeverity(result.Difference, "USD")
		exception := entities.NewReconciliationException(
			reportID,
			uuid.New(),
			entities.ReconciliationCheckLedgerConsistency,
			severity,
			"Ledger debits and credits do not balance",
			totalDebits,
			totalCredits,
			"USD",
		)
		exception.Metadata["total_debits"] = totalDebits.String()
		exception.Metadata["total_credits"] = totalCredits.String()
		result.Exceptions = append(result.Exceptions, *exception)
	}

	// 2. Check for orphaned entries
	orphanedCount, err := s.ledgerRepo.CountOrphanedEntries(ctx)
	if err != nil {
		s.logger.Error("Failed to check orphaned entries", "error", err)
	} else if orphanedCount > 0 {
		exception := entities.NewReconciliationException(
			reportID,
			uuid.New(),
			entities.ReconciliationCheckLedgerConsistency,
			entities.ExceptionSeverityHigh,
			fmt.Sprintf("Found %d orphaned ledger entries without matching transactions", orphanedCount),
			decimal.Zero,
			decimal.NewFromInt(int64(orphanedCount)),
			"count",
		)
		exception.Metadata["orphaned_count"] = orphanedCount
		result.Exceptions = append(result.Exceptions, *exception)
	}

	// 3. Check for transactions without exactly 2 entries
	invalidTxCount, err := s.ledgerRepo.CountInvalidTransactions(ctx)
	if err != nil {
		s.logger.Error("Failed to check invalid transactions", "error", err)
	} else if invalidTxCount > 0 {
		exception := entities.NewReconciliationException(
			reportID,
			uuid.New(),
			entities.ReconciliationCheckLedgerConsistency,
			entities.ExceptionSeverityHigh,
			fmt.Sprintf("Found %d transactions without exactly 2 entries", invalidTxCount),
			decimal.Zero,
			decimal.NewFromInt(int64(invalidTxCount)),
			"count",
		)
		exception.Metadata["invalid_transaction_count"] = invalidTxCount
		result.Exceptions = append(result.Exceptions, *exception)
	}

	result.Passed = len(result.Exceptions) == 0
	result.ExecutionTime = time.Since(startTime)

	span.SetAttributes(
		attribute.Bool("passed", result.Passed),
		attribute.Int("exceptions_count", len(result.Exceptions)),
	)

	return result, nil
}

// CheckCircleBalance verifies on-chain USDC buffer matches Circle wallet balances
func (s *Service) CheckCircleBalance(ctx context.Context, reportID uuid.UUID) (*entities.ReconciliationCheckResult, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "CheckCircleBalance")
	defer span.End()

	startTime := time.Now()
	result := &entities.ReconciliationCheckResult{
		CheckType:  entities.ReconciliationCheckCircleBalance,
		Exceptions: []entities.ReconciliationException{},
		Metadata:   make(map[string]interface{}),
	}

	// Get ledger balance for system_buffer_usdc
	ledgerBalance, err := s.ledgerService.GetSystemBufferBalance(ctx, "system_buffer_usdc")
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get ledger balance: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	// Get Circle wallet balances
	circleBalance, err := s.circleClient.GetTotalUSDCBalance(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get Circle balance: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	result.ExpectedValue = ledgerBalance
	result.ActualValue = circleBalance
	result.Difference = circleBalance.Sub(ledgerBalance)

	// Allow small tolerance for pending transactions (e.g., $10)
	tolerance := decimal.NewFromFloat(10.0)
	if result.Difference.Abs().GreaterThan(tolerance) {
		severity := entities.DetermineSeverity(result.Difference, "USDC")
		exception := entities.NewReconciliationException(
			reportID,
			uuid.New(),
			entities.ReconciliationCheckCircleBalance,
			severity,
			"Circle wallet balance does not match ledger system_buffer_usdc",
			ledgerBalance,
			circleBalance,
			"USDC",
		)
		exception.Metadata["ledger_balance"] = ledgerBalance.String()
		exception.Metadata["circle_balance"] = circleBalance.String()
		exception.Metadata["tolerance"] = tolerance.String()
		result.Exceptions = append(result.Exceptions, *exception)
	}

	result.Passed = len(result.Exceptions) == 0
	result.ExecutionTime = time.Since(startTime)

	span.SetAttributes(
		attribute.Bool("passed", result.Passed),
		attribute.String("ledger_balance", ledgerBalance.String()),
		attribute.String("circle_balance", circleBalance.String()),
	)

	return result, nil
}

// CheckAlpacaBalance verifies total user fiat exposure matches Alpaca buying power
func (s *Service) CheckAlpacaBalance(ctx context.Context, reportID uuid.UUID) (*entities.ReconciliationCheckResult, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "CheckAlpacaBalance")
	defer span.End()

	startTime := time.Now()
	result := &entities.ReconciliationCheckResult{
		CheckType:  entities.ReconciliationCheckAlpacaBalance,
		Exceptions: []entities.ReconciliationException{},
		Metadata:   make(map[string]interface{}),
	}

	// Get sum of all user fiat_exposure accounts
	totalFiatExposure, err := s.ledgerService.GetTotalUserFiatExposure(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get total fiat exposure: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	// Get total buying power from Alpaca
	alpacaBuyingPower, err := s.alpacaClient.GetTotalBuyingPower(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get Alpaca buying power: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	result.ExpectedValue = totalFiatExposure
	result.ActualValue = alpacaBuyingPower
	result.Difference = alpacaBuyingPower.Sub(totalFiatExposure)

	// Allow tolerance for pending orders
	tolerance := decimal.NewFromFloat(100.0)
	if result.Difference.Abs().GreaterThan(tolerance) {
		severity := entities.DetermineSeverity(result.Difference, "USD")
		exception := entities.NewReconciliationException(
			reportID,
			uuid.New(),
			entities.ReconciliationCheckAlpacaBalance,
			severity,
			"Alpaca buying power does not match ledger total fiat exposure",
			totalFiatExposure,
			alpacaBuyingPower,
			"USD",
		)
		exception.Metadata["ledger_fiat_exposure"] = totalFiatExposure.String()
		exception.Metadata["alpaca_buying_power"] = alpacaBuyingPower.String()
		exception.Metadata["tolerance"] = tolerance.String()
		result.Exceptions = append(result.Exceptions, *exception)
	}

	result.Passed = len(result.Exceptions) == 0
	result.ExecutionTime = time.Since(startTime)

	span.SetAttributes(
		attribute.Bool("passed", result.Passed),
		attribute.String("fiat_exposure", totalFiatExposure.String()),
		attribute.String("alpaca_buying_power", alpacaBuyingPower.String()),
	)

	return result, nil
}

// CheckDeposits verifies deposit amounts match ledger entries
func (s *Service) CheckDeposits(ctx context.Context, reportID uuid.UUID) (*entities.ReconciliationCheckResult, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "CheckDeposits")
	defer span.End()

	startTime := time.Now()
	result := &entities.ReconciliationCheckResult{
		CheckType:  entities.ReconciliationCheckDeposits,
		Exceptions: []entities.ReconciliationException{},
		Metadata:   make(map[string]interface{}),
	}

	// Get total of completed deposits from deposits table
	totalDeposits, err := s.depositRepo.GetTotalCompletedDeposits(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get total deposits: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	// Get total of deposit-related ledger entries
	totalLedgerDeposits, err := s.ledgerRepo.GetTotalDepositEntries(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get total ledger deposits: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	result.ExpectedValue = totalDeposits
	result.ActualValue = totalLedgerDeposits
	result.Difference = totalLedgerDeposits.Sub(totalDeposits)

	if !result.Difference.IsZero() {
		severity := entities.DetermineSeverity(result.Difference, "USDC")
		exception := entities.NewReconciliationException(
			reportID,
			uuid.New(),
			entities.ReconciliationCheckDeposits,
			severity,
			"Deposit table totals do not match ledger deposit entries",
			totalDeposits,
			totalLedgerDeposits,
			"USDC",
		)
		exception.Metadata["deposits_table_total"] = totalDeposits.String()
		exception.Metadata["ledger_deposits_total"] = totalLedgerDeposits.String()
		result.Exceptions = append(result.Exceptions, *exception)
	}

	result.Passed = len(result.Exceptions) == 0
	result.ExecutionTime = time.Since(startTime)

	span.SetAttributes(
		attribute.Bool("passed", result.Passed),
		attribute.String("deposits_total", totalDeposits.String()),
		attribute.String("ledger_total", totalLedgerDeposits.String()),
	)

	return result, nil
}

// CheckConversionJobs verifies all completed conversion jobs have ledger entries
func (s *Service) CheckConversionJobs(ctx context.Context, reportID uuid.UUID) (*entities.ReconciliationCheckResult, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "CheckConversionJobs")
	defer span.End()

	startTime := time.Now()
	result := &entities.ReconciliationCheckResult{
		CheckType:  entities.ReconciliationCheckConversionJobs,
		Exceptions: []entities.ReconciliationException{},
		Metadata:   make(map[string]interface{}),
	}

	// Get completed conversion jobs without ledger entries
	orphanedJobs, err := s.conversionRepo.GetCompletedJobsWithoutLedgerEntries(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to check conversion jobs: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	result.ExpectedValue = decimal.Zero
	result.ActualValue = decimal.NewFromInt(int64(len(orphanedJobs)))
	result.Difference = result.ActualValue

	if len(orphanedJobs) > 0 {
		for _, job := range orphanedJobs {
			exception := entities.NewReconciliationException(
				reportID,
				uuid.New(),
				entities.ReconciliationCheckConversionJobs,
				entities.ExceptionSeverityHigh,
				fmt.Sprintf("Conversion job %s completed but has no ledger entry", job.ID),
				decimal.Zero,
				job.Amount,
				"USD",
			)
			exception.AffectedEntity = job.ID.String()
			exception.Metadata["direction"] = string(job.Direction)
			if job.ProviderName != nil {
				exception.Metadata["provider"] = *job.ProviderName
			}
			exception.Metadata["completed_at"] = job.CompletedAt
			result.Exceptions = append(result.Exceptions, *exception)
		}
	}

	result.Passed = len(result.Exceptions) == 0
	result.ExecutionTime = time.Since(startTime)

	span.SetAttributes(
		attribute.Bool("passed", result.Passed),
		attribute.Int("orphaned_jobs", len(orphanedJobs)),
	)

	return result, nil
}

// CheckWithdrawals verifies withdrawal amounts match ledger entries
func (s *Service) CheckWithdrawals(ctx context.Context, reportID uuid.UUID) (*entities.ReconciliationCheckResult, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "CheckWithdrawals")
	defer span.End()

	startTime := time.Now()
	result := &entities.ReconciliationCheckResult{
		CheckType:  entities.ReconciliationCheckWithdrawals,
		Exceptions: []entities.ReconciliationException{},
		Metadata:   make(map[string]interface{}),
	}

	// Get total of completed withdrawals from withdrawals table
	totalWithdrawals, err := s.withdrawalRepo.GetTotalCompletedWithdrawals(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get total withdrawals: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	// Get total of withdrawal-related ledger entries
	totalLedgerWithdrawals, err := s.ledgerRepo.GetTotalWithdrawalEntries(ctx)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("failed to get total ledger withdrawals: %v", err)
		result.ExecutionTime = time.Since(startTime)
		span.RecordError(err)
		return result, err
	}

	result.ExpectedValue = totalWithdrawals
	result.ActualValue = totalLedgerWithdrawals
	result.Difference = totalLedgerWithdrawals.Sub(totalWithdrawals)

	if !result.Difference.IsZero() {
		severity := entities.DetermineSeverity(result.Difference, "USDC")
		exception := entities.NewReconciliationException(
			reportID,
			uuid.New(),
			entities.ReconciliationCheckWithdrawals,
			severity,
			"Withdrawal table totals do not match ledger withdrawal entries",
			totalWithdrawals,
			totalLedgerWithdrawals,
			"USDC",
		)
		exception.Metadata["withdrawals_table_total"] = totalWithdrawals.String()
		exception.Metadata["ledger_withdrawals_total"] = totalLedgerWithdrawals.String()
		result.Exceptions = append(result.Exceptions, *exception)
	}

	result.Passed = len(result.Exceptions) == 0
	result.ExecutionTime = time.Since(startTime)

	span.SetAttributes(
		attribute.Bool("passed", result.Passed),
		attribute.String("withdrawals_total", totalWithdrawals.String()),
		attribute.String("ledger_total", totalLedgerWithdrawals.String()),
	)

	return result, nil
}

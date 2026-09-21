package investment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Allocation-change execution (spec §7, §9, §17)
//
// Glider has no order API. A single-asset "buy" is therefore expressed as a new
// target weight and a deposit; a "sell" as a lower target weight. The provider
// then converges the portfolio on its next rebalance, and the execution record
// mirrors what actually happened rather than what we asked for.
// ---------------------------------------------------------------------------

// PlaceOrder changes one asset's target weight and (for buys) funds it.
func (s *Service) PlaceOrder(
	ctx context.Context,
	userID uuid.UUID,
	req *entities.InvestmentOrderRequest,
	actor entities.InvestmentActor,
) (*entities.InvestmentOrderResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil {
		return nil, fmt.Errorf("%w: missing order", ErrValidationFailed)
	}
	side := strings.ToLower(strings.TrimSpace(req.Side))
	if side != "buy" && side != "sell" {
		return nil, fmt.Errorf("%w: side must be buy or sell", ErrValidationFailed)
	}
	if !req.AmountUSD.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("%w: amount_usd must be greater than zero", ErrValidationFailed)
	}

	enrollment, strategy, version, err := s.resolveTradable(ctx, userID, req.StrategyID)
	if err != nil {
		return nil, err
	}
	asset, err := s.resolveAsset(ctx, req.AssetID, "", req.Symbol)
	if err != nil {
		return nil, fmt.Errorf("%w: unknown asset; try listing assets first", ErrValidationFailed)
	}

	// Idempotency: the same key never executes twice.
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = fmt.Sprintf("order-%s-%s-%s", userID.String(), asset.Symbol, side)
	}
	if existing, err := s.executions.FindByIdempotencyKey(ctx, key); err == nil && existing != nil {
		return &entities.InvestmentOrderResponse{Status: actionStatusFor(existing.Status), Execution: existing}, nil
	}

	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	target, err := s.shiftedAllocation(enrollment, version, asset, side, req.AmountUSD)
	if err != nil {
		return nil, err
	}
	report, err := s.validator.Validate(ctx, ValidationInput{
		Legs:              target,
		AmountUSD:         req.AmountUSD,
		Limits:            *limits,
		Constraints:       version.Constraints,
		PortfolioValueUSD: enrollment.TotalValueUSD,
	})
	if err != nil {
		return nil, err
	}
	if !report.Valid {
		return &entities.InvestmentOrderResponse{Status: entities.InvestmentActionRejected},
			fmt.Errorf("%w: %s", ErrValidationFailed, firstViolation(report))
	}

	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID:    userID,
		Action:    PolicyActionTrade,
		AmountUSD: req.AmountUSD,
		Risk:      strategy.Risk,
		Limits:    *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		return &entities.InvestmentOrderResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	holdings, err := s.holdings.ListByEnrollment(ctx, enrollment.ID)
	if err != nil {
		return nil, fmt.Errorf("list holdings: %w", err)
	}
	preview := s.previewer.Preview(PreviewInput{
		StrategyID:     strategy.ID.String(),
		Version:        version.Version,
		Target:         target,
		Current:        holdings,
		AmountUSD:      req.AmountUSD,
		Rules:          version.RebalanceRules,
		ExecutionRules: version.ExecutionRules,
		Policy:         decision,
	})

	// The confirmation binds to the proposal, never to the token itself, so the
	// token field is cleared before the payload is hashed.
	sanitized := *req
	sanitized.ConfirmationToken = ""
	outcome, err := s.confirmMutation(ctx, userID, orderAction(side), sanitized, decision, preview, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	if !outcome.Proceed {
		return &entities.InvestmentOrderResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Preview:      preview,
			Policy:       decision,
			Confirmation: outcome.Pending,
		}, nil
	}

	execution := s.newExecution(userID, enrollment, strategy, version.Version, entities.InvestmentExecutionTrade, side, asset, req.AmountUSD, key, decision, actor)
	execution.MarketDataAsOf = &preview.MarketDataAsOf
	execution.ConfirmationMethod = "payload_hash_confirmation"
	if err := s.executions.Create(ctx, execution); err != nil {
		return nil, fmt.Errorf("create execution: %w", err)
	}

	// Buys need cash in the portfolio first; sells release cash and need no
	// funding leg.
	if side == "buy" {
		if _, err := s.fundEnrollment(ctx, userID, enrollment, req.AmountUSD, normaliseFundingSource(""), key, actor); err != nil {
			return s.failExecution(ctx, execution, "funding_failed", err)
		}
	}

	if err := s.publishAndRebalance(ctx, userID, enrollment, strategy, version, report.NormalizedAllocation, actor, map[string]any{
		"reason":       fmt.Sprintf("%s %s", side, asset.Symbol),
		"execution_id": execution.ID.String(),
	}); err != nil {
		return s.failExecution(ctx, execution, "provider_rejected", err)
	}

	// Refresh holdings so the caller sees the funded portfolio instead of the
	// pre-trade snapshot. Reads never call the provider; mutations may.
	if err := s.SyncEnrollment(ctx, enrollment.ID); err != nil {
		s.log.Error("could not refresh portfolio after order",
			"enrollment_id", enrollment.ID.String(),
			"execution_id", execution.ID.String(),
			"error", err)
	}

	execution.Status = entities.InvestmentExecutionSubmitted
	execution.UpdatedAt = s.nowOr()
	if err := s.executions.Update(ctx, execution); err != nil {
		return nil, fmt.Errorf("update execution: %w", err)
	}
	_ = s.recordEvent(ctx, userID, EventOrderSubmitted, actor, map[string]any{
		"order_id": execution.ID.String(),
		"side":     side,
		"symbol":   asset.Symbol,
		"amount":   req.AmountUSD.String(),
		"note":     "the provider converges the portfolio on its next rebalance; fills are reported back, not assumed",
	})
	return &entities.InvestmentOrderResponse{
		Status:    entities.InvestmentActionCompleted,
		Execution: execution,
		Preview:   preview,
		Policy:    decision,
	}, nil
}

// PlaceMultiOrder replaces the whole target allocation of a user's strategy.
func (s *Service) PlaceMultiOrder(
	ctx context.Context,
	userID uuid.UUID,
	req *entities.InvestmentMultiOrderRequest,
	actor entities.InvestmentActor,
) (*entities.InvestmentOrderResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil || len(req.Targets) == 0 {
		return nil, fmt.Errorf("%w: provide the full target allocation", ErrValidationFailed)
	}
	enrollment, strategy, version, err := s.resolveTradable(ctx, userID, req.StrategyID)
	if err != nil {
		return nil, err
	}
	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	report, err := s.validator.Validate(ctx, ValidationInput{
		Legs:              req.Targets,
		Limits:            *limits,
		Constraints:       version.Constraints,
		PortfolioValueUSD: enrollment.TotalValueUSD,
	})
	if err != nil {
		return nil, err
	}
	if !report.Valid {
		return &entities.InvestmentOrderResponse{Status: entities.InvestmentActionRejected},
			fmt.Errorf("%w: %s", ErrValidationFailed, firstViolation(report))
	}

	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID: userID,
		Action: PolicyActionTrade,
		Risk:   strategy.Risk,
		Limits: *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		return &entities.InvestmentOrderResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	holdings, err := s.holdings.ListByEnrollment(ctx, enrollment.ID)
	if err != nil {
		return nil, fmt.Errorf("list holdings: %w", err)
	}
	preview := s.previewer.Preview(PreviewInput{
		StrategyID:     strategy.ID.String(),
		Version:        version.Version,
		Target:         report.NormalizedAllocation,
		Current:        holdings,
		Rules:          version.RebalanceRules,
		ExecutionRules: version.ExecutionRules,
		Policy:         decision,
	})

	sanitized := *req
	sanitized.ConfirmationToken = ""
	outcome, err := s.confirmMutation(ctx, userID, "set_allocation", sanitized, decision, preview, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	if !outcome.Proceed {
		return &entities.InvestmentOrderResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Preview:      preview,
			Policy:       decision,
			Confirmation: outcome.Pending,
		}, nil
	}

	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = fmt.Sprintf("set-allocation-%s-%d", userID.String(), version.Version+1)
	}
	execution := s.newExecution(userID, enrollment, strategy, version.Version, entities.InvestmentExecutionRebalance, "", nil, decimal.Zero, key, decision, actor)
	execution.ConfirmationMethod = "payload_hash_confirmation"
	execution.MarketDataAsOf = &preview.MarketDataAsOf
	if err := s.executions.Create(ctx, execution); err != nil {
		return nil, fmt.Errorf("create execution: %w", err)
	}

	if err := s.publishAndRebalance(ctx, userID, enrollment, strategy, version, report.NormalizedAllocation, actor, map[string]any{
		"reason":       "target allocation changed",
		"rationale":    req.Rationale,
		"execution_id": execution.ID.String(),
	}); err != nil {
		return s.failExecution(ctx, execution, "provider_rejected", err)
	}

	execution.Status = entities.InvestmentExecutionSubmitted
	execution.UpdatedAt = s.nowOr()
	if err := s.executions.Update(ctx, execution); err != nil {
		return nil, fmt.Errorf("update execution: %w", err)
	}
	return &entities.InvestmentOrderResponse{
		Status:    entities.InvestmentActionCompleted,
		Execution: execution,
		Preview:   preview,
		Policy:    decision,
	}, nil
}

// TriggerRebalance asks the provider to converge a portfolio now.
//
// The provider rate-limits this (429 + Retry-After); the cooldown is surfaced
// as ErrProviderCooldown so the caller can be told when to try again.
func (s *Service) TriggerRebalance(
	ctx context.Context,
	userID, strategyID uuid.UUID,
	reason string,
	actor entities.InvestmentActor,
) (*entities.InvestmentExecution, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	enrollment, strategy, version, err := s.resolveTradable(ctx, userID, strategyID.String())
	if err != nil {
		return nil, err
	}
	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID: userID,
		Action: PolicyActionRebalance,
		Risk:   strategy.Risk,
		Limits: *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		return nil, fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	// Rebalance attempts are deduplicated by the provider's own cooldown, so the
	// local key only has to be unique per attempt.
	key := fmt.Sprintf("rebalance-%s-%s", enrollment.ID.String(), uuid.NewString())
	if existing, err := s.executions.FindByIdempotencyKey(ctx, key); err == nil && existing != nil {
		return existing, nil
	}
	execution := s.newExecution(userID, enrollment, strategy, version.Version, entities.InvestmentExecutionRebalance, "", nil, decimal.Zero, key, decision, actor)
	if err := s.executions.Create(ctx, execution); err != nil {
		return nil, fmt.Errorf("create execution: %w", err)
	}

	handle, err := s.provider.TriggerRebalance(ctx, enrollment.GliderPortfolioID)
	if err != nil {
		mapped := s.mapProviderError(err)
		execution.Status = entities.InvestmentExecutionFailed
		execution.FailureCode = "provider_rejected"
		execution.FailureReason = mapped.Error()
		execution.UpdatedAt = s.nowOr()
		if updateErr := s.executions.Update(ctx, execution); updateErr != nil {
			s.log.Error("failed to persist failed rebalance", "error", updateErr)
		}
		return nil, mapped
	}

	execution.Status = entities.InvestmentExecutionSubmitted
	execution.ProviderOperationID = handle.OperationID
	execution.UpdatedAt = s.nowOr()
	if err := s.executions.Update(ctx, execution); err != nil {
		return nil, fmt.Errorf("update execution: %w", err)
	}
	if err := s.trackOperation(ctx, userID, &enrollment.ID, &execution.ID, handle.OperationID, "rebalance"); err != nil {
		return nil, err
	}
	now := s.nowOr()
	enrollment.LastRebalanceAt = &now
	enrollment.Status = entities.InvestmentEnrollmentActive
	enrollment.UpdatedAt = now
	if err := s.enrollments.Update(ctx, enrollment); err != nil {
		return nil, fmt.Errorf("update enrollment: %w", err)
	}
	_ = s.recordStrategyEvent(ctx, userID, EventRebalanceExecuted, strategy, actor, map[string]any{
		"order_id":     execution.ID.String(),
		"operation_id": handle.OperationID,
		"reason":       reason,
		"trigger":      actor,
	})
	return execution, nil
}

// ---------------------------------------------------------------------------
// Withdrawal (spec §13, §19): interactive, Rails-signed, never chat-only
// ---------------------------------------------------------------------------

// Withdraw takes money out of a portfolio. It requires a verified step-up
// (the HTTP layer only calls it with one) and the portfolio owner's signature.
//
// stepUpVerified is not a hint: without it the service returns the policy
// verdict and refuses.
func (s *Service) Withdraw(
	ctx context.Context,
	userID uuid.UUID,
	req *entities.InvestmentWithdrawalRequest,
	stepUpVerified bool,
	actor entities.InvestmentActor,
) (*entities.InvestmentWithdrawalResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil {
		return nil, fmt.Errorf("%w: missing withdrawal", ErrValidationFailed)
	}
	enrollment, strategy, version, err := s.resolveWithdrawable(ctx, userID, req.StrategyID)
	if err != nil {
		return nil, err
	}

	amount := req.AmountUSD
	if req.LiquidateAll {
		amount = enrollment.TotalValueUSD
	}
	if !req.LiquidateAll && !amount.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("%w: amount_usd must be greater than zero", ErrValidationFailed)
	}

	// Retirement lock (fail closed). A vault-linked portfolio has exactly one way
	// out: a single-use authorization issued by the vault. Without a valid one,
	// or without the vault wired at all, the withdrawal is refused before any
	// provider call is made.
	recipientOverride := ""
	if enrollment.VaultID != nil {
		if s.vaultObserver == nil {
			return &entities.InvestmentWithdrawalResponse{Status: entities.InvestmentActionRejected},
				fmt.Errorf("%w: this portfolio is locked to a retirement plan and cannot be withdrawn from here", ErrPolicyBlocked)
		}
		plan, authErr := s.vaultObserver.Authorize(ctx, enrollment.ID, amount, req.VaultAuthorizationKey)
		if authErr != nil {
			_ = s.recordEvent(ctx, userID, EventPolicyBlocked, actor, map[string]any{
				"action":        "withdraw",
				"enrollment_id": enrollment.ID.String(),
				"reason":        "vault authorization refused",
				"error":         authErr.Error(),
			})
			return &entities.InvestmentWithdrawalResponse{Status: entities.InvestmentActionRejected},
				fmt.Errorf("%w: %v", ErrPolicyBlocked, authErr)
		}
		if plan == nil || strings.TrimSpace(plan.SettlementAccount) == "" {
			return &entities.InvestmentWithdrawalResponse{Status: entities.InvestmentActionRejected},
				fmt.Errorf("%w: the retirement plan did not provide a settlement account", ErrPolicyBlocked)
		}
		recipientOverride = plan.SettlementAccount
	}

	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	action := PolicyActionWithdraw
	if req.LiquidateAll {
		action = PolicyActionLiquidate
	}
	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID:    userID,
		Action:    action,
		AmountUSD: amount,
		Risk:      strategy.Risk,
		Limits:    *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		return &entities.InvestmentWithdrawalResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}
	if !stepUpVerified {
		_ = s.recordStrategyEvent(ctx, userID, EventWithdrawalRequested, strategy, actor, map[string]any{
			"amount":  amount.String(),
			"blocked": "requires step-up authentication",
		})
		return &entities.InvestmentWithdrawalResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrStepUpRequired, strings.Join(decision.Reasons, "; "))
	}

	if enrollment.TotalValueUSD.GreaterThan(decimal.Zero) && amount.GreaterThan(enrollment.TotalValueUSD) {
		return &entities.InvestmentWithdrawalResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: the portfolio is worth %s", ErrValidationFailed, enrollment.TotalValueUSD.StringFixed(2))
	}

	recipient := recipientOverride
	if recipient == "" {
		if s.funding != nil {
			recipient, err = s.funding.RecipientAccount(ctx, userID)
			if err != nil {
				return nil, fmt.Errorf("resolve withdrawal recipient: %w", err)
			}
		}
	}
	if strings.TrimSpace(recipient) == "" {
		return nil, fmt.Errorf("%w: no rail-controlled settlement account is configured", ErrUnsupported)
	}

	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = fmt.Sprintf("withdraw-%s-%.2f", enrollment.ID.String(), amount.InexactFloat64())
	}
	if existing, err := s.executions.FindByIdempotencyKey(ctx, key); err == nil && existing != nil {
		return &entities.InvestmentWithdrawalResponse{
			Status:      actionStatusFor(existing.Status),
			Execution:   existing,
			Policy:      existing.Policy,
			Recipient:   recipient,
			OperationID: existing.ProviderOperationID,
		}, nil
	}

	kind := entities.InvestmentExecutionWithdraw
	if req.LiquidateAll {
		kind = entities.InvestmentExecutionLiquidate
	}
	execution := s.newExecution(userID, enrollment, strategy, version.Version, kind, "sell", nil, amount, key, decision, actor)
	if err := s.executions.Create(ctx, execution); err != nil {
		return nil, fmt.Errorf("create execution: %w", err)
	}
	_ = s.recordStrategyEvent(ctx, userID, EventWithdrawalRequested, strategy, actor, map[string]any{
		"amount":    amount.String(),
		"order_id":  execution.ID.String(),
		"liquidate": req.LiquidateAll,
		"step_up":   true,
	})

	withdrawAssets := []entities.GliderWithdrawAsset{}
	if !req.LiquidateAll {
		asset, err := s.resolveAsset(ctx, req.AssetID, "", req.Symbol)
		if err != nil {
			return s.failWithdrawal(ctx, execution, "unknown_asset", fmt.Errorf("%w: unknown asset", ErrValidationFailed))
		}
		withdrawAssets = append(withdrawAssets, entities.GliderWithdrawAsset{
			AssetID:   asset.CAIP19,
			AmountRaw: amount.Shift(int32(asset.Decimals)).Truncate(0).String(),
		})
	}

	authorization, err := s.provider.PrepareWithdrawal(ctx, enrollment.GliderPortfolioID, entities.GliderWithdrawSignatureInput{
		RecipientAccountID: recipient,
		Assets:             withdrawAssets,
		Liquidate:          req.LiquidateAll,
	})
	if err != nil {
		return s.failWithdrawal(ctx, execution, "provider_rejected", s.mapProviderError(err))
	}

	request := &entities.InvestmentSignatureRequest{
		ID:             uuid.New(),
		UserID:         userID,
		EnrollmentID:   &enrollment.ID,
		Flow:           "withdraw",
		ProviderFlowID: authorization.AuthorizationID,
		Status:         "prepared",
		ExpiresAt:      authorization.ExpiresAt,
		CreatedAt:      s.nowOr(),
		UpdatedAt:      s.nowOr(),
	}
	if s.signatures != nil {
		if err := s.signatures.Create(ctx, request); err != nil {
			return nil, fmt.Errorf("record signature request: %w", err)
		}
	}
	if s.signer == nil {
		return s.failWithdrawal(ctx, execution, "no_signer", fmt.Errorf("%w: no portfolio owner signer is configured", ErrUnsupported))
	}

	message := authorization.Message
	authorizationJSON := ""
	if authorization.Authorization != nil {
		if message == "" {
			message = authorization.Authorization.Text
		}
		authorizationJSON = authorization.Authorization.Raw
	}
	signed, err := s.signer.SignSolanaMessage(ctx, userID, firstNonEmpty(message, authorizationJSON))
	if err != nil {
		s.markSignatureFailed(ctx, request, err)
		return s.failWithdrawal(ctx, execution, "signature_failed", err)
	}

	handle, err := s.provider.SubmitWithdrawal(ctx, enrollment.GliderPortfolioID, entities.GliderWithdrawSubmitInput{
		Message:            firstNonEmpty(message, authorizationJSON),
		Signature:          signed,
		RecipientAccountID: recipient,
		Assets:             withdrawAssets,
		Liquidate:          req.LiquidateAll,
		SettlementAssetID:  "",
	})
	if err != nil {
		s.markSignatureFailed(ctx, request, err)
		return s.failWithdrawal(ctx, execution, "provider_rejected", s.mapProviderError(err))
	}

	if s.signatures != nil {
		request.Status = "submitted"
		request.UpdatedAt = s.nowOr()
		_ = s.signatures.Update(ctx, request)
	}

	execution.Status = entities.InvestmentExecutionSubmitted
	execution.ProviderOperationID = handle.OperationID
	execution.UpdatedAt = s.nowOr()
	if err := s.executions.Update(ctx, execution); err != nil {
		return nil, fmt.Errorf("update execution: %w", err)
	}

	// The money-out leg is recorded now; the ledger is credited by the existing
	// crypto-deposit path once the USDC actually lands, so no invented balance
	// movements are created here.
	if s.transfers != nil {
		now := s.nowOr()
		transfer := &entities.InvestmentFundingTransfer{
			ID:                   uuid.New(),
			UserID:               userID,
			EnrollmentID:         enrollment.ID,
			Direction:            "withdrawal",
			AmountUSD:            amount,
			Asset:                "USDC",
			SourceAccount:        enrollment.GliderPortfolioID,
			DestinationAccountID: recipient,
			Status:               "SUBMITTED",
			IdempotencyKey:       key,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		if err := s.transfers.Create(ctx, transfer); err != nil {
			s.log.Error("failed to record withdrawal transfer", "error", err)
		}
	}
	if err := s.trackOperation(ctx, userID, &enrollment.ID, &execution.ID, handle.OperationID, "withdrawal"); err != nil {
		return nil, err
	}
	_ = s.recordEvent(ctx, userID, EventWithdrawalRequested, actor, map[string]any{
		"order_id":     execution.ID.String(),
		"operation_id": handle.OperationID,
		"amount":       amount.String(),
		"recipient":    recipient,
		"step_up":      true,
	})
	return &entities.InvestmentWithdrawalResponse{
		Status:      entities.InvestmentActionCompleted,
		Policy:      decision,
		Execution:   execution,
		Recipient:   recipient,
		OperationID: handle.OperationID,
	}, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// resolveTradable loads an enrollment, its strategy and the current version and
// refuses strategies whose allocation is shared with other portfolios: an
// allocation change on a shared template would move everyone's money.
func (s *Service) resolveTradable(
	ctx context.Context,
	userID uuid.UUID,
	strategyID string,
) (*entities.InvestmentEnrollment, *entities.InvestmentStrategy, *entities.InvestmentStrategyVersion, error) {
	enrollment, err := s.findActiveEnrollment(ctx, userID, strategyID)
	if err != nil {
		return nil, nil, nil, err
	}
	strategy, err := s.strategies.GetByID(ctx, enrollment.StrategyID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get strategy: %w", err)
	}
	if strategy == nil || !s.canMutate(strategy, userID) {
		return nil, nil, nil, fmt.Errorf("%w: this portfolio mirrors a shared strategy, so its allocation cannot be changed from here; create your own strategy instead", ErrUnsupported)
	}
	version, err := s.strategies.GetVersion(ctx, strategy.ID, strategy.CurrentVersion)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get strategy version: %w", err)
	}
	if version == nil {
		return nil, nil, nil, ErrNotFound
	}
	return enrollment, strategy, version, nil
}

// resolveWithdrawable is resolveTradable for withdrawals. A vault-linked
// portfolio mirrors a Rail-owned strategy, which users cannot mutate — but they
// must still be able to withdraw from it. The lock that governs that withdrawal
// is enforced by the vault observer, not by the mutability rule.
func (s *Service) resolveWithdrawable(
	ctx context.Context,
	userID uuid.UUID,
	strategyID string,
) (*entities.InvestmentEnrollment, *entities.InvestmentStrategy, *entities.InvestmentStrategyVersion, error) {
	enrollment, err := s.findActiveEnrollment(ctx, userID, strategyID)
	if err != nil {
		return nil, nil, nil, err
	}
	strategy, err := s.strategies.GetByID(ctx, enrollment.StrategyID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get strategy: %w", err)
	}
	if strategy == nil {
		return nil, nil, nil, ErrNotFound
	}
	if enrollment.VaultID == nil && !s.canMutate(strategy, userID) {
		return nil, nil, nil, fmt.Errorf("%w: this portfolio mirrors a shared strategy, so it cannot be withdrawn from here; create your own strategy instead", ErrUnsupported)
	}
	version, err := s.strategies.GetVersion(ctx, strategy.ID, strategy.CurrentVersion)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get strategy version: %w", err)
	}
	if version == nil {
		return nil, nil, nil, ErrNotFound
	}
	return enrollment, strategy, version, nil
}

// findActiveEnrollment locates the user's active or paused portfolio, scoped to
// a strategy id when one is supplied.
func (s *Service) findActiveEnrollment(
	ctx context.Context,
	userID uuid.UUID,
	strategyID string,
) (*entities.InvestmentEnrollment, error) {
	enrollments, err := s.enrollments.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list enrollments: %w", err)
	}
	var enrollment *entities.InvestmentEnrollment
	for _, candidate := range enrollments {
		if candidate.Status != entities.InvestmentEnrollmentActive && candidate.Status != entities.InvestmentEnrollmentPaused {
			continue
		}
		if strings.TrimSpace(strategyID) == "" || candidate.StrategyID.String() == strings.TrimSpace(strategyID) {
			enrollment = candidate
			break
		}
	}
	if enrollment == nil {
		if strings.TrimSpace(strategyID) == "" {
			return nil, fmt.Errorf("%w: you are not enrolled in an investment strategy yet", ErrNotFound)
		}
		return nil, fmt.Errorf("%w: no active portfolio for that strategy", ErrNotFound)
	}
	if strings.TrimSpace(strategyID) == "" && len(enrollments) > 1 {
		return nil, fmt.Errorf("%w: you have more than one portfolio; pass strategy_id", ErrValidationFailed)
	}
	return enrollment, nil
}

// shiftedAllocation returns the version's allocation with one asset's weight
// moved by the order size. The other legs absorb the difference so the weights
// always sum to exactly 100.
func (s *Service) shiftedAllocation(
	enrollment *entities.InvestmentEnrollment,
	version *entities.InvestmentStrategyVersion,
	asset *entities.InvestmentAsset,
	side string,
	amountUSD decimal.Decimal,
) ([]entities.InvestmentAllocationLeg, error) {
	legs := cloneLegs(version.TargetAllocation)
	if len(legs) == 0 {
		return nil, fmt.Errorf("%w: this strategy has no allocation", ErrNotFound)
	}

	value := enrollment.TotalValueUSD
	if !value.GreaterThan(decimal.Zero) {
		// Without a valuation we cannot express an amount as a weight; the user
		// must fund the portfolio first.
		return nil, fmt.Errorf("%w: the portfolio has no value yet, so an order cannot be sized", ErrValidationFailed)
	}
	totalAfter := value
	if side == "buy" {
		totalAfter = value.Add(amountUSD)
	}
	if !totalAfter.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("%w: the portfolio has no value yet", ErrValidationFailed)
	}

	targetIndex := -1
	currentValue := decimal.Zero
	for i, leg := range legs {
		if strings.EqualFold(leg.Symbol, asset.Symbol) || leg.CAIP19 == asset.CAIP19 || leg.AssetID == asset.ID.String() {
			targetIndex = i
			break
		}
	}
	if targetIndex >= 0 {
		currentValue = value.Mul(legs[targetIndex].Weight).Div(decimal.NewFromInt(100))
	}

	newValue := currentValue.Add(amountUSD)
	if side == "sell" {
		newValue = currentValue.Sub(amountUSD)
		if newValue.IsNegative() {
			newValue = decimal.Zero
		}
	}
	newWeight := newValue.Div(totalAfter).Mul(decimal.NewFromInt(100)).Round(2)
	if newWeight.GreaterThan(decimal.NewFromInt(100)) {
		newWeight = decimal.NewFromInt(100)
	}

	if targetIndex >= 0 {
		legs[targetIndex].Weight = newWeight
	} else {
		legs = append(legs, entities.InvestmentAllocationLeg{
			AssetID: asset.ID.String(),
			CAIP19:  asset.CAIP19,
			Symbol:  asset.Symbol,
			Weight:  newWeight,
		})
	}

	// Rebalance the remaining legs proportionally to their old weights so the
	// total is exactly 100.
	remaining := decimal.NewFromInt(100).Sub(newWeight)
	othersSum := decimal.Zero
	for i := range legs {
		if i == targetIndex || (targetIndex < 0 && i == len(legs)-1) {
			continue
		}
		othersSum = othersSum.Add(legs[i].Weight)
	}
	for i := range legs {
		if i == targetIndex || (targetIndex < 0 && i == len(legs)-1) {
			continue
		}
		if othersSum.IsZero() {
			// Only one other leg exists: it takes the rest.
			legs[i].Weight = remaining
			continue
		}
		legs[i].Weight = legs[i].Weight.Div(othersSum).Mul(remaining).Round(2)
	}
	return legs, nil
}

// publishAndRebalance publishes the new allocation to the provider and asks for
// an immediate convergence. It is the only place a live portfolio's target is
// changed.
func (s *Service) publishAndRebalance(
	ctx context.Context,
	userID uuid.UUID,
	enrollment *entities.InvestmentEnrollment,
	strategy *entities.InvestmentStrategy,
	version *entities.InvestmentStrategyVersion,
	allocation []entities.InvestmentAllocationLeg,
	actor entities.InvestmentActor,
	payload map[string]any,
) error {
	if strategy.GliderStrategyID == nil || *strategy.GliderStrategyID == "" {
		return fmt.Errorf("%w: this strategy has no provider binding", ErrUnsupported)
	}
	published, err := s.provider.PublishStrategyVersion(ctx, *strategy.GliderStrategyID, entities.GliderStrategyInput{
		Allocation:  entities.GliderAllocation{Assets: allocation},
		Schedule:    &entities.GliderSchedule{Type: "interval", Frequency: providerFrequency(version.RebalanceRules)},
		Preferences: s.preferencesFor(version.ExecutionRules),
	})
	if err != nil {
		return s.mapProviderError(err)
	}

	now := s.nowOr()
	newVersion := &entities.InvestmentStrategyVersion{
		ID:                uuid.New(),
		StrategyID:        strategy.ID,
		Version:           version.Version + 1,
		TargetAllocation:  allocation,
		Risk:              strategy.Risk,
		Horizon:           strategy.Horizon,
		RebalanceRules:    version.RebalanceRules,
		ContributionRules: version.ContributionRules,
		ExecutionRules:    version.ExecutionRules,
		Constraints:       version.Constraints,
		Rationale:         stringOf(payload["reason"]),
		CreatedBy:         actor,
		CreatedAt:         now,
	}
	if published.Version > 0 {
		counter := published.Version
		newVersion.GliderStrategyVersion = &counter
	}
	if err := s.strategies.CreateVersion(ctx, newVersion); err != nil {
		return fmt.Errorf("store strategy version: %w", err)
	}
	strategy.CurrentVersion = newVersion.Version
	strategy.UpdatedAt = now
	if err := s.strategies.Update(ctx, strategy); err != nil {
		return fmt.Errorf("update strategy: %w", err)
	}

	handle, err := s.provider.TriggerRebalance(ctx, enrollment.GliderPortfolioID)
	if err != nil {
		// The allocation change itself succeeded; the convergence is deferred to
		// the provider's schedule. That is reported, not hidden.
		s.log.Error("allocation changed but manual rebalance was refused",
			"enrollment_id", enrollment.ID.String(),
			"error", err)
		return nil
	}
	_ = s.trackOperation(ctx, userID, &enrollment.ID, nil, handle.OperationID, "rebalance")
	enrollment.LastRebalanceAt = &now
	enrollment.UpdatedAt = now
	if err := s.enrollments.Update(ctx, enrollment); err != nil {
		return fmt.Errorf("update enrollment: %w", err)
	}
	return nil
}

func (s *Service) newExecution(
	userID uuid.UUID,
	enrollment *entities.InvestmentEnrollment,
	strategy *entities.InvestmentStrategy,
	version int,
	kind entities.InvestmentExecutionKind,
	side string,
	asset *entities.InvestmentAsset,
	amount decimal.Decimal,
	key string,
	decision *entities.InvestmentPolicyDecision,
	actor entities.InvestmentActor,
) *entities.InvestmentExecution {
	now := s.nowOr()
	enrollmentID := enrollment.ID
	execution := &entities.InvestmentExecution{
		ID:                 uuid.New(),
		UserID:             userID,
		EnrollmentID:       &enrollmentID,
		Kind:               kind,
		Side:               side,
		RequestedAmountUSD: amount,
		ValidatedAmountUSD: amount,
		Status:             entities.InvestmentExecutionRequested,
		IdempotencyKey:     key,
		Policy:             decision,
		Provider:           "glider",
		RequestedBy:        actor,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if strategy != nil {
		strategyID := strategy.ID
		execution.StrategyID = &strategyID
		execution.StrategyVersion = &version
	}
	if asset != nil {
		assetID := asset.ID
		execution.AssetID = &assetID
		execution.Symbol = asset.Symbol
	}
	return execution
}

func (s *Service) failExecution(ctx context.Context, execution *entities.InvestmentExecution, code string, cause error) (*entities.InvestmentOrderResponse, error) {
	execution.Status = entities.InvestmentExecutionFailed
	execution.FailureCode = code
	execution.FailureReason = cause.Error()
	execution.UpdatedAt = s.nowOr()
	if err := s.executions.Update(ctx, execution); err != nil {
		s.log.Error("failed to persist failed execution", "error", err)
	}
	_ = s.recordEvent(ctx, execution.UserID, EventOrderFailed, execution.RequestedBy, map[string]any{
		"order_id": execution.ID.String(),
		"code":     code,
		"reason":   cause.Error(),
	})
	return &entities.InvestmentOrderResponse{
		Status:    entities.InvestmentActionRejected,
		Execution: execution,
		Policy:    execution.Policy,
	}, cause
}

func (s *Service) failWithdrawal(ctx context.Context, execution *entities.InvestmentExecution, code string, cause error) (*entities.InvestmentWithdrawalResponse, error) {
	execution.Status = entities.InvestmentExecutionFailed
	execution.FailureCode = code
	execution.FailureReason = cause.Error()
	execution.UpdatedAt = s.nowOr()
	if err := s.executions.Update(ctx, execution); err != nil {
		s.log.Error("failed to persist failed withdrawal", "error", err)
	}
	return &entities.InvestmentWithdrawalResponse{
		Status:    entities.InvestmentActionRejected,
		Execution: execution,
		Policy:    execution.Policy,
	}, cause
}

// trackOperation mirrors a provider operation so the worker can poll it.
func (s *Service) trackOperation(
	ctx context.Context,
	userID uuid.UUID,
	enrollmentID, executionID *uuid.UUID,
	operationID, kind string,
) error {
	if s.operations == nil || strings.TrimSpace(operationID) == "" {
		return nil
	}
	now := s.nowOr()
	operation := &entities.GliderOperation{
		ID:                  uuid.New(),
		UserID:              userID,
		EnrollmentID:        enrollmentID,
		ExecutionID:         executionID,
		ProviderOperationID: operationID,
		Kind:                kind,
		State:               "accepted",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := s.operations.Upsert(ctx, operation); err != nil {
		return fmt.Errorf("track operation: %w", err)
	}
	return nil
}

func orderAction(side string) string {
	if side == "sell" {
		return "sell_asset"
	}
	return "buy_asset"
}

func actionStatusFor(status entities.InvestmentExecutionStatus) entities.InvestmentActionStatus {
	switch status {
	case entities.InvestmentExecutionFilled:
		return entities.InvestmentActionCompleted
	case entities.InvestmentExecutionFailed, entities.InvestmentExecutionCancelled:
		return entities.InvestmentActionRejected
	default:
		return entities.InvestmentActionAwaitingConfirmation
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringOf(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// ErrStepUpRequired means the action is allowed but needs in-app step-up
// authentication, which a messaging channel cannot provide.
var ErrStepUpRequired = errors.New("investment: step-up authentication required")

package investment

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Enroll enrolls a user into a strategy and (optionally) funds the new
// portfolio.
//
// Two-stage owner authorization happens here, not in the agent: the provider
// returns a payload to sign, the configured OwnerSigner signs it, and only then
// does the portfolio exist. Every stage is recorded so an authorization can
// never be replayed silently.
func (s *Service) Enroll(
	ctx context.Context,
	userID uuid.UUID,
	req *entities.InvestmentEnrollRequest,
	actor entities.InvestmentActor,
) (*entities.InvestmentEnrollResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil || strings.TrimSpace(req.StrategyID) == "" {
		return nil, fmt.Errorf("%w: strategy_id is required", ErrValidationFailed)
	}
	strategyID, err := uuid.Parse(req.StrategyID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid strategy id", ErrValidationFailed)
	}

	strategy, err := s.strategies.GetByID(ctx, strategyID)
	if err != nil {
		return nil, fmt.Errorf("get strategy: %w", err)
	}
	if strategy == nil || !s.canAccess(strategy, userID) {
		return nil, ErrNotFound
	}
	version, err := s.strategies.GetVersion(ctx, strategy.ID, strategy.CurrentVersion)
	if err != nil {
		return nil, fmt.Errorf("get strategy version: %w", err)
	}
	if version == nil {
		return nil, fmt.Errorf("%w: this strategy has no published allocation", ErrNotFound)
	}

	// Idempotent re-enroll: an active enrollment is returned as-is.
	if existing, err := s.enrollments.GetByUserAndStrategy(ctx, userID, strategy.ID); err != nil {
		return nil, fmt.Errorf("check enrollment: %w", err)
	} else if existing != nil && existing.Status == entities.InvestmentEnrollmentActive {
		return &entities.InvestmentEnrollResponse{
			Status:     entities.InvestmentActionCompleted,
			Enrollment: existing,
		}, nil
	}

	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	if limits.MaxEnrollments > 0 {
		enrollments, err := s.enrollments.ListByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("list enrollments: %w", err)
		}
		if len(enrollments) >= limits.MaxEnrollments {
			return &entities.InvestmentEnrollResponse{
					Status: entities.InvestmentActionRejected,
				},
				fmt.Errorf("%w: this account can hold at most %d investment strategies", ErrPolicyBlocked, limits.MaxEnrollments)
		}
	}

	amount := req.AmountUSD
	value, err := s.portfolioValue(ctx, userID)
	if err != nil {
		return nil, err
	}
	report, err := s.validator.Validate(ctx, ValidationInput{
		Legs:              version.TargetAllocation,
		AmountUSD:         amount,
		Limits:            *limits,
		Constraints:       version.Constraints,
		PortfolioValueUSD: value,
	})
	if err != nil {
		return nil, err
	}
	if !report.Valid {
		return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionRejected},
			fmt.Errorf("%w: %s", ErrValidationFailed, firstViolation(report))
	}

	if amount.GreaterThan(decimal.Zero) && s.funding != nil {
		source := normaliseFundingSource(req.Source)
		available, err := s.funding.Available(ctx, userID, source)
		if err != nil {
			return nil, fmt.Errorf("check available balance: %w", err)
		}
		if available.LessThan(amount) {
			return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionRejected},
				fmt.Errorf("%w: you have %s available from %s, which is less than %s",
					ErrPolicyBlocked, available.StringFixed(2), source, amount.StringFixed(2))
		}
	}

	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID:    userID,
		Action:    PolicyActionEnroll,
		AmountUSD: amount,
		Risk:      strategy.Risk,
		Limits:    *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		_ = s.recordStrategyEvent(ctx, userID, EventPolicyBlocked, strategy, actor, map[string]any{
			"action": "enroll",
			"reason": decision.Reasons,
		})
		return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	var preview *entities.InvestmentAllocationPreview
	if amount.GreaterThan(decimal.Zero) {
		holdings, err := s.holdings.ListByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("list holdings: %w", err)
		}
		preview = s.previewer.Preview(PreviewInput{
			StrategyID:     strategy.ID.String(),
			Version:        version.Version,
			Target:         version.TargetAllocation,
			Current:        holdings,
			AmountUSD:      amount,
			Rules:          version.RebalanceRules,
			ExecutionRules: version.ExecutionRules,
			Policy:         decision,
		})
	}

	// The confirmation binds to the proposal, never to the token itself, so the
	// token field is cleared before the payload is hashed.
	sanitized := *req
	sanitized.ConfirmationToken = ""
	outcome, err := s.confirmMutation(ctx, userID, "enroll_strategy", sanitized, decision, preview, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	if !outcome.Proceed {
		return &entities.InvestmentEnrollResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Preview:      preview,
			Policy:       decision,
			Confirmation: outcome.Pending,
		}, nil
	}

	if decision.Verdict == entities.InvestmentVerdictRequiresAuthentication {
		return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	enrollment, err := s.enrollWithProvider(ctx, userID, strategy, version)
	if err != nil {
		return nil, err
	}

	_ = s.recordStrategyEvent(ctx, userID, EventStrategyEnrolled, strategy, actor, map[string]any{
		"enrollment_id":      enrollment.ID.String(),
		"provider_portfolio": enrollment.GliderPortfolioID,
		"version":            version.Version,
		"chain":              enrollment.Chain,
	})

	var funding *entities.InvestmentFundingTransfer
	if amount.GreaterThan(decimal.Zero) {
		funding, err = s.fundEnrollment(ctx, userID, enrollment, amount, normaliseFundingSource(req.Source), req.IdempotencyKey, actor)
		if err != nil {
			// The portfolio exists and is auditable; the funding leg failed and
			// is reported honestly rather than swallowed. FAILED + non-nil
			// error so handlers answer 5xx with the reason on the body —
			// never a silent COMPLETED the client mistakes for funded.
			s.log.Error("enrollment funded later: funding leg failed",
				"enrollment_id", enrollment.ID.String(),
				"user_id", userID.String(),
				"error", err)
			return &entities.InvestmentEnrollResponse{
				Status:     entities.InvestmentActionFailed,
				Enrollment: enrollment,
				Preview:    preview,
				Policy:     decision,
				Funding: &entities.InvestmentFundingTransfer{
					EnrollmentID:  enrollment.ID,
					Direction:     "deposit",
					AmountUSD:     amount,
					Status:        "FAILED",
					FailureReason: err.Error(),
				},
			}, fmt.Errorf("enrollment funding failed: %w", err)
		}
		// Refresh positions so the first answer after enrollment is grounded.
		_ = s.SyncEnrollment(ctx, enrollment.ID)
	}

	return &entities.InvestmentEnrollResponse{
		Status:     entities.InvestmentActionCompleted,
		Enrollment: enrollment,
		Preview:    preview,
		Policy:     decision,
		Funding:    funding,
	}, nil
}

// enrollWithProvider performs the two-stage owner authorization and persists
// the enrollment.
func (s *Service) enrollWithProvider(
	ctx context.Context,
	userID uuid.UUID,
	strategy *entities.InvestmentStrategy,
	version *entities.InvestmentStrategyVersion,
) (*entities.InvestmentEnrollment, error) {
	if strategy.GliderStrategyID == nil || *strategy.GliderStrategyID == "" {
		return nil, fmt.Errorf("%w: this strategy has no provider binding", ErrValidationFailed)
	}
	if s.signer == nil {
		return nil, fmt.Errorf("%w: no portfolio owner signer is configured", ErrUnsupported)
	}

	ownerAccount, err := s.signer.OwnerAccount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("resolve owner account: %w", err)
	}

	authorization, err := s.provider.PrepareEnrollment(ctx, entities.GliderEnrollSignatureInput{
		StrategyID:     *strategy.GliderStrategyID,
		OwnerAccountID: ownerAccount,
		ChainIDs:       s.cfg.SolanaChainIDs,
		// Solana Model B (and Model A) must NOT send accountType: the provider
		// returns a Solana transaction / slot-bound payload instead of an
		// ECDSA message. Sending "ECDSA" here breaks Solana enrollment.
	})
	if err != nil {
		return nil, fmt.Errorf("prepare enrollment: %w", s.mapProviderError(err))
	}

	request := &entities.InvestmentSignatureRequest{
		ID:             uuid.New(),
		UserID:         userID,
		Flow:           "enroll",
		ProviderFlowID: authorization.FlowID,
		Status:         "prepared",
		ExpiresAt:      s.signatureExpiry(),
		CreatedAt:      s.nowOr(),
		UpdatedAt:      s.nowOr(),
	}
	if s.signatures != nil {
		if err := s.signatures.Create(ctx, request); err != nil {
			return nil, fmt.Errorf("record signature request: %w", err)
		}
	}

	submit := entities.GliderEnrollSubmitInput{
		FlowID:         authorization.FlowID,
		AccountIndex:   authorization.AccountIndex,
		AgentAccountID: authorization.AgentAccountID,
		ChainIDs:       s.cfg.SolanaChainIDs,
		AccountType:    authorization.AccountType,
		// Stage 2 must echo the owner and strategy used to open the flow.
		OwnerAccountID: ownerAccount,
		StrategyID:     *strategy.GliderStrategyID,
	}

	switch {
	case strings.TrimSpace(authorization.SolanaTransaction) != "":
		// Solana-rooted owner (Model B): stage 1 returned a serialized Solana
		// transaction the owner signs with their wallet key.
		signed, err := s.signer.SignSolanaTransaction(ctx, userID, authorization.SolanaTransaction)
		if err != nil {
			s.markSignatureFailed(ctx, request, err)
			return nil, fmt.Errorf("sign enrollment authorization: %w", err)
		}
		submit.SignedSolanaTransaction = signed
	case authorization.Message != nil && strings.TrimSpace(authorization.Message.Text) != "":
		signed, err := s.signer.SignSolanaMessage(ctx, userID, authorization.Message.Text)
		if err != nil {
			s.markSignatureFailed(ctx, request, err)
			return nil, fmt.Errorf("sign enrollment authorization: %w", err)
		}
		submit.Signature = signed
	case authorization.Message != nil && strings.TrimSpace(authorization.Message.Raw) != "":
		// EVM-rooted owners (Model A) get an informational digest plus a
		// slot-bound Swig authorization the client must build and sign; Rail's
		// signer cannot produce one, and signing the digest itself is invalid.
		s.markSignatureFailed(ctx, request, fmt.Errorf("unsupported enrollment flow"))
		return nil, fmt.Errorf("%w: this enrollment needs an EVM-rooted authorization Rail does not build; the portfolio owner must be Solana-rooted", ErrUnsupported)
	default:
		return nil, fmt.Errorf("%w: the provider returned nothing to sign", ErrUnsupported)
	}

	if s.signatures != nil {
		request.Status = "signed"
		request.UpdatedAt = s.nowOr()
		_ = s.signatures.Update(ctx, request)
	}

	portfolio, err := s.provider.SubmitEnrollment(ctx, submit)
	if err != nil {
		s.markSignatureFailed(ctx, request, err)
		return nil, fmt.Errorf("submit enrollment: %w", s.mapProviderError(err))
	}

	now := s.nowOr()
	enrollment := &entities.InvestmentEnrollment{
		ID:                uuid.New(),
		UserID:            userID,
		StrategyID:        strategy.ID,
		StrategyVersion:   version.Version,
		GliderPortfolioID: portfolio.PortfolioID,
		GliderStrategyID:  *strategy.GliderStrategyID,
		Chain:             s.chainFor(portfolio),
		OwnerAccountID:    ownerAccount,
		AgentAccountID:    authorization.AgentAccountID,
		DepositAccountID:  depositAccountFor(portfolio),
		Status:            entities.InvestmentEnrollmentActive,
		AutomationStatus:  "active",
		NextDueAt:         portfolio.Schedule.NextDueAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if role := swigRoleFor(portfolio); role != nil {
		enrollment.SwigRoleID = role
	}
	if err := s.enrollments.Create(ctx, enrollment); err != nil {
		return nil, fmt.Errorf("store enrollment: %w", err)
	}

	if s.signatures != nil {
		request.Status = "submitted"
		request.EnrollmentID = &enrollment.ID
		request.UpdatedAt = s.nowOr()
		_ = s.signatures.Update(ctx, request)
	}

	// The strategy becomes active because a portfolio now mirrors it.
	if strategy.Status != entities.InvestmentStrategyActive {
		_ = s.transitionStrategy(ctx, strategy, entities.InvestmentStrategyActive)
	}
	return enrollment, nil
}

// Fund moves additional money into an existing portfolio. It is the top-up
// primitive the retirement vault uses for contributions: enrollment already
// created the portfolio, so this only has to move and record the money.
func (s *Service) Fund(
	ctx context.Context,
	userID, enrollmentID uuid.UUID,
	amountUSD decimal.Decimal,
	source, idempotencyKey string,
	actor entities.InvestmentActor,
) (*entities.InvestmentFundingTransfer, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if !amountUSD.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("%w: amount_usd must be greater than zero", ErrValidationFailed)
	}
	enrollment, err := s.enrollments.GetByID(ctx, enrollmentID)
	if err != nil {
		return nil, fmt.Errorf("get enrollment: %w", err)
	}
	if enrollment == nil || enrollment.UserID != userID {
		return nil, ErrNotFound
	}
	if enrollment.Status == entities.InvestmentEnrollmentClosed {
		return nil, fmt.Errorf("%w: this portfolio is closed", ErrValidationFailed)
	}

	resolved := normaliseFundingSource(source)
	if s.funding != nil {
		available, err := s.funding.Available(ctx, userID, resolved)
		if err != nil {
			return nil, fmt.Errorf("check available balance: %w", err)
		}
		if available.LessThan(amountUSD) {
			return nil, fmt.Errorf("%w: you have %s available to save, which is less than %s",
				ErrPolicyBlocked, available.StringFixed(2), amountUSD.StringFixed(2))
		}
	}
	return s.fundEnrollment(ctx, userID, enrollment, amountUSD, resolved, idempotencyKey, actor)
}

// fundEnrollment moves money into the portfolio and records both legs.
func (s *Service) fundEnrollment(
	ctx context.Context,
	userID uuid.UUID,
	enrollment *entities.InvestmentEnrollment,
	amount decimal.Decimal,
	source, idempotencyKey string,
	actor entities.InvestmentActor,
) (*entities.InvestmentFundingTransfer, error) {
	if enrollment.DepositAccountID == "" {
		return nil, fmt.Errorf("%w: the portfolio has no deposit account", ErrUnsupported)
	}
	if s.funding == nil {
		return nil, fmt.Errorf("%w: no funding path is configured", ErrUnsupported)
	}

	key := strings.TrimSpace(idempotencyKey)
	if key == "" {
		key = fmt.Sprintf("invest-enroll-%s", enrollment.ID.String())
	}
	if s.transfers != nil {
		if existing, err := s.transfers.FindByIdempotencyKey(ctx, key); err == nil && existing != nil {
			return existing, nil
		}
	}

	now := s.nowOr()
	transfer := &entities.InvestmentFundingTransfer{
		ID:                   uuid.New(),
		UserID:               userID,
		EnrollmentID:         enrollment.ID,
		Direction:            "deposit",
		AmountUSD:            amount,
		Asset:                "USDC",
		SourceAccount:        source,
		DestinationAccountID: enrollment.DepositAccountID,
		Status:               "PENDING",
		IdempotencyKey:       key,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if s.transfers != nil {
		if err := s.transfers.Create(ctx, transfer); err != nil {
			return nil, fmt.Errorf("record funding transfer: %w", err)
		}
	}
	_ = s.recordEvent(ctx, userID, EventFundingInitiated, actor, map[string]any{
		"enrollment_id": enrollment.ID.String(),
		"amount_usd":    amount.String(),
		"source":        source,
		"transfer_id":   transfer.ID.String(),
	})

	result, err := s.funding.TransferToPortfolio(ctx, FundingRequest{
		UserID:         userID,
		EnrollmentID:   enrollment.ID,
		AmountUSD:      amount,
		Source:         source,
		Destination:    enrollment.DepositAccountID,
		IdempotencyKey: key,
	})
	if err != nil {
		transfer.Status = "FAILED"
		transfer.FailureReason = err.Error()
		transfer.UpdatedAt = s.nowOr()
		if s.transfers != nil {
			_ = s.transfers.Update(ctx, transfer)
		}
		return nil, err
	}

	transfer.Status = result.Status
	if transfer.Status == "" {
		transfer.Status = "SUBMITTED"
	}
	transfer.LedgerTransactionID = result.LedgerTransactionID
	transfer.OnchainTxRef = result.OnchainTxRef
	transfer.UpdatedAt = s.nowOr()
	if s.transfers != nil {
		if err := s.transfers.Update(ctx, transfer); err != nil {
			return nil, fmt.Errorf("update funding transfer: %w", err)
		}
	}
	_ = s.recordEvent(ctx, userID, EventFundingSettled, actor, map[string]any{
		"enrollment_id": enrollment.ID.String(),
		"amount_usd":    amount.String(),
		"status":        transfer.Status,
		"onchain_ref":   transfer.OnchainTxRef,
	})
	return transfer, nil
}

func (s *Service) markSignatureFailed(ctx context.Context, request *entities.InvestmentSignatureRequest, cause error) {
	if request == nil || s.signatures == nil {
		return
	}
	request.Status = "failed"
	request.FailureReason = cause.Error()
	request.UpdatedAt = s.nowOr()
	_ = s.signatures.Update(ctx, request)
}

func (s *Service) chainFor(portfolio *entities.GliderPortfolio) string {
	for _, account := range portfolio.SmartAccounts {
		if strings.HasPrefix(account.AccountID, "solana:") || strings.HasPrefix(account.DepositAccountID, "solana:") {
			return "solana"
		}
	}
	return s.cfg.DefaultChain
}

func depositAccountFor(portfolio *entities.GliderPortfolio) string {
	for _, account := range portfolio.SmartAccounts {
		if account.DepositAccountID != "" {
			return account.DepositAccountID
		}
	}
	if len(portfolio.SmartAccounts) > 0 {
		return portfolio.SmartAccounts[0].AccountID
	}
	return ""
}

func swigRoleFor(portfolio *entities.GliderPortfolio) *int {
	for _, account := range portfolio.SmartAccounts {
		if account.SwigRoleID != nil {
			return account.SwigRoleID
		}
	}
	return nil
}

// normaliseFundingSource maps caller wording onto the two ledger sources the
// funding leg understands. Spending is the default: stash money can be locked,
// so defaulting to stash would fail the lock check for most users.
func normaliseFundingSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "stash", "yield":
		return "stash"
	default:
		return "spending"
	}
}

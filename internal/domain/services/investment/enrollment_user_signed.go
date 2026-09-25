package investment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// UserEnrollPrepareRequest is the caller-supplied intent for a user-signed
// (Solana Model B) enrollment. The wallet address comes from the user's own
// wallet — Rail never invents it — and must be a solana: CAIP-10 id.
type UserEnrollPrepareRequest struct {
	StrategyID        string          `json:"strategy_id"`
	OwnerAccountID    string          `json:"owner_account_id"`
	AmountUSD         decimal.Decimal `json:"amount_usd,omitempty"`
	Source            string          `json:"source,omitempty"`
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
}

// UserEnrollPrepareResponse returns the sign payload plus card fields.
// Nothing is created on-chain until Complete is called with the signed tx.
type UserEnrollPrepareResponse struct {
	Status       entities.InvestmentActionStatus `json:"status"`
	FlowID       string                          `json:"flow_id,omitempty"`
	AccountIndex string                          `json:"account_index,omitempty"`
	AgentAccount string                          `json:"agent_account_id,omitempty"`
	OwnerAccount string                          `json:"owner_account_id,omitempty"`
	StrategyID   string                          `json:"strategy_id,omitempty"`
	ChainIDs     []int                           `json:"chain_ids,omitempty"`
	SignPayload  string                          `json:"sign_payload,omitempty"` // base64 solanaTransaction
	Message      string                          `json:"message,omitempty"`      // informational text, if any
	DepositHint  string                          `json:"deposit_account_id,omitempty"`
	// AlreadyEnrolled is set when this user already has an active portfolio
	// for the strategy. There is nothing new to sign; the caller funds it.
	AlreadyEnrolled bool                                  `json:"already_enrolled,omitempty"`
	EnrollmentID    string                                `json:"enrollment_id,omitempty"`
	Preview         *entities.InvestmentAllocationPreview `json:"preview,omitempty"`
	Policy          *entities.InvestmentPolicyDecision    `json:"policy,omitempty"`
	Confirmation    *entities.InvestmentPendingAction     `json:"confirmation,omitempty"`
}

// UserContributeRequest funds an existing portfolio. It is the top-up after
// the first user-signed enrollment: USDC moves from the Rail balance to the
// portfolio deposit account. No new wallet signature is required.
type UserContributeRequest struct {
	StrategyID        string          `json:"strategy_id"`
	AmountUSD         decimal.Decimal `json:"amount_usd"`
	Source            string          `json:"source,omitempty"`
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	ConfirmationToken string          `json:"confirmation_token,omitempty"`
}

// UserContributeResponse is the staged or settled funding result.
type UserContributeResponse struct {
	Status       entities.InvestmentActionStatus     `json:"status"`
	Enrollment   *entities.InvestmentEnrollment      `json:"enrollment,omitempty"`
	Funding      *entities.InvestmentFundingTransfer `json:"funding,omitempty"`
	Policy       *entities.InvestmentPolicyDecision  `json:"policy,omitempty"`
	Confirmation *entities.InvestmentPendingAction   `json:"confirmation,omitempty"`
}

// UserEnrollCompleteRequest submits the wallet-signed transaction.
// Round-trip fields must echo stage 1 byte-for-byte; the confirmation token
// binds flowId + amount + strategyId and rejects mutated payloads.
type UserEnrollCompleteRequest struct {
	StrategyID              string          `json:"strategy_id"`
	OwnerAccountID          string          `json:"owner_account_id"`
	FlowID                  string          `json:"flow_id"`
	AccountIndex            string          `json:"account_index"`
	AgentAccountID          string          `json:"agent_account_id"`
	ChainIDs                []int           `json:"chain_ids"`
	SignedSolanaTransaction string          `json:"signed_solana_transaction"`
	AmountUSD               decimal.Decimal `json:"amount_usd,omitempty"`
	Source                  string          `json:"source,omitempty"`
	IdempotencyKey          string          `json:"idempotency_key,omitempty"`
	ConfirmationToken       string          `json:"confirmation_token,omitempty"`
}

// PrepareUserEnrollment runs Glider stage 1 (POST /enroll/signature) for a
// user-held Solana wallet and stages the confirmation the allocate card is
// bound to. Fail-closed: any provider error aborts before a sign payload is
// returned; production always calls the real HTTP provider.
//
// This is confirmation 1 of 2 for a user-signed enrollment. It binds the
// intent (strategy + owner + amount + source) before any provider flow exists.
// Confirmation 2 happens in CompleteUserEnrollment, which additionally binds
// the provider flowId: the user confirms the intent first, signs in their
// wallet, then confirms the exact signed submission. Agent callers must drive
// both stages; a prepare token is never valid for complete and vice versa.
func (s *Service) PrepareUserEnrollment(
	ctx context.Context,
	userID uuid.UUID,
	req *UserEnrollPrepareRequest,
	actor entities.InvestmentActor,
) (*UserEnrollPrepareResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil || strings.TrimSpace(req.StrategyID) == "" {
		return nil, fmt.Errorf("%w: strategy_id is required", ErrValidationFailed)
	}
	if !strings.HasPrefix(strings.TrimSpace(req.OwnerAccountID), "solana:") {
		return nil, fmt.Errorf("%w: owner_account_id must be a solana: CAIP-10 id", ErrValidationFailed)
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
	if strategy.GliderStrategyID == nil || *strategy.GliderStrategyID == "" {
		return nil, fmt.Errorf("%w: this strategy has no provider binding", ErrValidationFailed)
	}
	if existing, err := s.enrollments.GetByUserAndStrategy(ctx, userID, strategy.ID); err != nil {
		return nil, fmt.Errorf("check enrollment: %w", err)
	} else if existing != nil && existing.Status == entities.InvestmentEnrollmentActive {
		return &UserEnrollPrepareResponse{
			Status:          entities.InvestmentActionCompleted,
			StrategyID:      req.StrategyID,
			AlreadyEnrolled: true,
			EnrollmentID:    existing.ID.String(),
		}, nil
	}

	version, err := s.strategies.GetVersion(ctx, strategy.ID, strategy.CurrentVersion)
	if err != nil {
		return nil, fmt.Errorf("get strategy version: %w", err)
	}
	if version == nil {
		return nil, fmt.Errorf("%w: this strategy has no published allocation", ErrNotFound)
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
			return &UserEnrollPrepareResponse{
					Status: entities.InvestmentActionRejected,
				},
				fmt.Errorf("%w: this account can hold at most %d investment strategies", ErrPolicyBlocked, limits.MaxEnrollments)
		}
	}
	value, err := s.portfolioValue(ctx, userID)
	if err != nil {
		return nil, err
	}
	report, err := s.validator.Validate(ctx, ValidationInput{
		Legs:              version.TargetAllocation,
		AmountUSD:         req.AmountUSD,
		Limits:            *limits,
		Constraints:       version.Constraints,
		PortfolioValueUSD: value,
	})
	if err != nil {
		return nil, err
	}
	if !report.Valid {
		return &UserEnrollPrepareResponse{Status: entities.InvestmentActionRejected},
			fmt.Errorf("%w: %s", ErrValidationFailed, firstViolation(report))
	}
	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID:    userID,
		Action:    PolicyActionEnroll,
		AmountUSD: req.AmountUSD,
		Risk:      strategy.Risk,
		Limits:    *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		return &UserEnrollPrepareResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	var preview *entities.InvestmentAllocationPreview
	if req.AmountUSD.GreaterThan(decimal.Zero) {
		holdings, err := s.holdings.ListByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("list holdings: %w", err)
		}
		preview = s.previewer.Preview(PreviewInput{
			StrategyID:     strategy.ID.String(),
			Version:        version.Version,
			Target:         version.TargetAllocation,
			Current:        holdings,
			AmountUSD:      req.AmountUSD,
			Rules:          version.RebalanceRules,
			ExecutionRules: version.ExecutionRules,
			Policy:         decision,
		})
	}

	// The card binds to flow-independent fields at prepare time (strategy +
	// amount + owner). The flowId is added to the binding at complete time.
	type prepareBinding struct {
		StrategyID     string `json:"strategy_id"`
		OwnerAccountID string `json:"owner_account_id"`
		AmountUSD      string `json:"amount_usd"`
		Source         string `json:"source"`
	}
	binding := prepareBinding{
		StrategyID:     req.StrategyID,
		OwnerAccountID: strings.TrimSpace(req.OwnerAccountID),
		AmountUSD:      req.AmountUSD.String(),
		Source:         normaliseFundingSource(req.Source),
	}
	outcome, err := s.confirmMutation(ctx, userID, "enroll_user_signed_prepare", binding, decision, preview, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	if !outcome.Proceed {
		return &UserEnrollPrepareResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Preview:      preview,
			Policy:       decision,
			Confirmation: outcome.Pending,
		}, nil
	}

	if decision.Verdict == entities.InvestmentVerdictRequiresAuthentication {
		return &UserEnrollPrepareResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	authorization, err := s.provider.PrepareEnrollment(ctx, entities.GliderEnrollSignatureInput{
		StrategyID:     *strategy.GliderStrategyID,
		OwnerAccountID: strings.TrimSpace(req.OwnerAccountID),
		ChainIDs:       s.cfg.SolanaChainIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare enrollment: %w", s.mapProviderError(err))
	}
	if strings.TrimSpace(authorization.SolanaTransaction) == "" {
		return nil, fmt.Errorf("%w: the provider returned no Solana transaction to sign", ErrUnsupported)
	}

	roundTrip, _ := json.Marshal(map[string]any{
		"strategy_id":      req.StrategyID,
		"glider_strategy":  *strategy.GliderStrategyID,
		"owner_account_id": strings.TrimSpace(req.OwnerAccountID),
		"flow_id":          authorization.FlowID,
		"account_index":    authorization.AccountIndex,
		"agent_account_id": authorization.AgentAccountID,
		"chain_ids":        s.cfg.SolanaChainIDs,
		"amount_usd":       req.AmountUSD.String(),
		"source":           normaliseFundingSource(req.Source),
	})
	if s.signatures != nil {
		_ = s.signatures.Create(ctx, &entities.InvestmentSignatureRequest{
			ID:             uuid.New(),
			UserID:         userID,
			Flow:           "enroll_user_signed",
			ProviderFlowID: authorization.FlowID,
			Payload:        roundTrip,
			Status:         "prepared",
			ExpiresAt:      s.signatureExpiry(),
			CreatedAt:      s.nowOr(),
			UpdatedAt:      s.nowOr(),
		})
	}

	message := ""
	if authorization.Message != nil {
		message = authorization.Message.Text
	}
	return &UserEnrollPrepareResponse{
		Status:       entities.InvestmentActionCompleted,
		FlowID:       authorization.FlowID,
		AccountIndex: authorization.AccountIndex,
		AgentAccount: authorization.AgentAccountID,
		OwnerAccount: strings.TrimSpace(req.OwnerAccountID),
		StrategyID:   req.StrategyID,
		ChainIDs:     s.cfg.SolanaChainIDs,
		SignPayload:  authorization.SolanaTransaction,
		Message:      message,
		DepositHint:  authorization.DepositAccountID,
		Preview:      preview,
		Policy:       decision,
	}, nil
}

// CompleteUserEnrollment verifies the confirmation binding, submits the
// user-signed transaction (stage 2, idempotent on flowId), persists the
// enrollment, starts automation, and funds when an amount was bound.
//
// This is confirmation 2 of 2 (see PrepareUserEnrollment): the token staged
// here binds strategy + owner + flowId + amount + source, so anything the
// wallet signed that differs from the confirmed intent is rejected before it
// reaches the provider.
func (s *Service) CompleteUserEnrollment(
	ctx context.Context,
	userID uuid.UUID,
	req *UserEnrollCompleteRequest,
	actor entities.InvestmentActor,
) (*entities.InvestmentEnrollResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil || strings.TrimSpace(req.FlowID) == "" {
		return nil, fmt.Errorf("%w: flow_id is required", ErrValidationFailed)
	}
	if strings.TrimSpace(req.SignedSolanaTransaction) == "" {
		return nil, fmt.Errorf("%w: signed_solana_transaction is required", ErrValidationFailed)
	}
	if strings.TrimSpace(req.AccountIndex) == "" {
		return nil, fmt.Errorf("%w: account_index must echo stage 1 byte-for-byte", ErrValidationFailed)
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
	if existing, err := s.enrollments.GetByUserAndStrategy(ctx, userID, strategy.ID); err != nil {
		return nil, fmt.Errorf("check enrollment: %w", err)
	} else if existing != nil && existing.Status == entities.InvestmentEnrollmentActive {
		return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionCompleted, Enrollment: existing}, nil
	}

	// Confirmation binds flowId + amount + strategyId: mutated payloads are
	// rejected here, never submitted to the provider.
	type completeBinding struct {
		StrategyID     string `json:"strategy_id"`
		OwnerAccountID string `json:"owner_account_id"`
		FlowID         string `json:"flow_id"`
		AmountUSD      string `json:"amount_usd"`
		Source         string `json:"source"`
	}
	binding := completeBinding{
		StrategyID:     req.StrategyID,
		OwnerAccountID: strings.TrimSpace(req.OwnerAccountID),
		FlowID:         strings.TrimSpace(req.FlowID),
		AmountUSD:      req.AmountUSD.String(),
		Source:         normaliseFundingSource(req.Source),
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
	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID:    userID,
		Action:    PolicyActionEnroll,
		AmountUSD: req.AmountUSD,
		Risk:      strategy.Risk,
		Limits:    *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview {
		return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}
	if req.AmountUSD.GreaterThan(decimal.Zero) && s.funding != nil {
		source := normaliseFundingSource(req.Source)
		available, err := s.funding.Available(ctx, userID, source)
		if err != nil {
			return nil, fmt.Errorf("check available balance: %w", err)
		}
		if available.LessThan(req.AmountUSD) {
			return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionRejected},
				fmt.Errorf("%w: you have %s available from %s, which is less than %s",
					ErrPolicyBlocked, available.StringFixed(2), source, req.AmountUSD.StringFixed(2))
		}
	}
	outcome, err := s.confirmMutation(ctx, userID, "enroll_user_signed_complete", binding, decision, nil, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	if !outcome.Proceed {
		return &entities.InvestmentEnrollResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Policy:       decision,
			Confirmation: outcome.Pending,
		}, nil
	}

	if decision.Verdict == entities.InvestmentVerdictRequiresAuthentication {
		return &entities.InvestmentEnrollResponse{Status: entities.InvestmentActionRejected, Policy: decision},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	var stored *entities.InvestmentSignatureRequest
	if s.signatures != nil {
		stored, _ = s.signatures.FindByFlow(ctx, "enroll_user_signed", strings.TrimSpace(req.FlowID))
	}
	if stored != nil && stored.UserID != userID {
		return nil, fmt.Errorf("%w: this enrollment was prepared by a different account", ErrConfirmationInvalid)
	}
	var expected map[string]any
	if stored != nil && len(stored.Payload) > 0 {
		_ = json.Unmarshal(stored.Payload, &expected)
	}
	chainIDs := req.ChainIDs
	if expected != nil {
		// Byte-for-byte round-trip guard: every field the provider bound at
		// stage 1 must echo here unchanged, otherwise the caller is driving a
		// different enrollment through this flow's authorization.
		if v, _ := expected["account_index"].(string); v != "" && v != req.AccountIndex {
			return nil, fmt.Errorf("%w: account_index changed since stage 1", ErrConfirmationInvalid)
		}
		if v, _ := expected["agent_account_id"].(string); v != "" && v != req.AgentAccountID {
			return nil, fmt.Errorf("%w: agent_account_id changed since stage 1", ErrConfirmationInvalid)
		}
		if v, _ := expected["owner_account_id"].(string); v != "" && v != strings.TrimSpace(req.OwnerAccountID) {
			return nil, fmt.Errorf("%w: owner_account_id changed since stage 1", ErrConfirmationInvalid)
		}
		if v, _ := expected["strategy_id"].(string); v != "" && v != strategy.ID.String() {
			return nil, fmt.Errorf("%w: strategy_id changed since stage 1; prepare again", ErrConfirmationInvalid)
		}
		if v, _ := expected["glider_strategy"].(string); v != "" && v != gliderStrategyIDOf(strategy) {
			return nil, fmt.Errorf("%w: the provider strategy changed since stage 1; prepare again", ErrConfirmationInvalid)
		}
		preparedChains := decodeChainIDs(expected["chain_ids"])
		if len(chainIDs) == 0 {
			chainIDs = preparedChains
		} else if len(preparedChains) > 0 && !chainIDsEqual(chainIDs, preparedChains) {
			return nil, fmt.Errorf("%w: chain_ids changed since stage 1", ErrConfirmationInvalid)
		}
	}
	if len(chainIDs) == 0 {
		chainIDs = s.cfg.SolanaChainIDs
	}

	portfolio, err := s.provider.SubmitEnrollment(ctx, entities.GliderEnrollSubmitInput{
		FlowID:                  strings.TrimSpace(req.FlowID),
		AccountIndex:            req.AccountIndex,
		AgentAccountID:          req.AgentAccountID,
		ChainIDs:                chainIDs,
		OwnerAccountID:          strings.TrimSpace(req.OwnerAccountID),
		StrategyID:              gliderStrategyIDOf(strategy),
		SignedSolanaTransaction: req.SignedSolanaTransaction,
	})
	if err != nil {
		if stored != nil && s.signatures != nil {
			stored.Status = "failed"
			stored.FailureReason = err.Error()
			stored.UpdatedAt = s.nowOr()
			_ = s.signatures.Update(ctx, stored)
		}
		return nil, fmt.Errorf("submit enrollment: %w", s.mapProviderError(err))
	}

	version, err := s.strategies.GetVersion(ctx, strategy.ID, strategy.CurrentVersion)
	if err != nil || version == nil {
		return nil, fmt.Errorf("%w: strategy version unavailable", ErrNotFound)
	}
	now := s.nowOr()
	enrollment := &entities.InvestmentEnrollment{
		ID:                uuid.New(),
		UserID:            userID,
		StrategyID:        strategy.ID,
		StrategyVersion:   version.Version,
		GliderPortfolioID: portfolio.PortfolioID,
		GliderStrategyID:  gliderStrategyIDOf(strategy),
		Chain:             s.chainFor(portfolio),
		OwnerAccountID:    strings.TrimSpace(req.OwnerAccountID),
		AgentAccountID:    req.AgentAccountID,
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
	if stored != nil && s.signatures != nil {
		stored.Status = "submitted"
		stored.EnrollmentID = &enrollment.ID
		stored.UpdatedAt = s.nowOr()
		_ = s.signatures.Update(ctx, stored)
	}
	if strategy.Status != entities.InvestmentStrategyActive {
		_ = s.transitionStrategy(ctx, strategy, entities.InvestmentStrategyActive)
	}

	// Start automation (best effort) so the sleeve re-targets after funding.
	if s.provider != nil {
		_ = s.provider.StartPortfolio(ctx, portfolio.PortfolioID)
	}

	var funding *entities.InvestmentFundingTransfer
	if req.AmountUSD.GreaterThan(decimal.Zero) {
		// The funding leg is keyed on the provider flowId (the idempotency
		// anchor for this enrollment), never on the freshly minted enrollment
		// row: a retry after a crash between submit and persist must find the
		// first transfer, not debit a second time.
		key := strings.TrimSpace(req.IdempotencyKey)
		if key == "" {
			key = fmt.Sprintf("invest-enroll-user-%s", strings.TrimSpace(req.FlowID))
		}
		funding, err = s.fundEnrollment(ctx, userID, enrollment, req.AmountUSD, normaliseFundingSource(req.Source), key, actor)
		if err != nil {
			s.log.Error("user enrollment funding leg failed",
				"enrollment_id", enrollment.ID.String(), "error", err)
			return &entities.InvestmentEnrollResponse{
				Status:     entities.InvestmentActionFailed,
				Enrollment: enrollment,
				Policy:     decision,
				Funding: &entities.InvestmentFundingTransfer{
					EnrollmentID:  enrollment.ID,
					Direction:     "deposit",
					AmountUSD:     req.AmountUSD,
					Status:        "FAILED",
					FailureReason: err.Error(),
				},
			}, fmt.Errorf("enrollment funding failed: %w", err)
		}
		_ = s.SyncEnrollment(ctx, enrollment.ID)
	}

	_ = s.recordStrategyEvent(ctx, userID, EventStrategyEnrolled, strategy, actor, map[string]any{
		"enrollment_id":      enrollment.ID.String(),
		"provider_portfolio": enrollment.GliderPortfolioID,
		"flow":               "enroll_user_signed",
		"chain":              enrollment.Chain,
	})
	return &entities.InvestmentEnrollResponse{
		Status:     entities.InvestmentActionCompleted,
		Enrollment: enrollment,
		Policy:     decision,
		Funding:    funding,
	}, nil
}

// Contribute moves more USDC into a portfolio the user is already enrolled in.
// The first enrollment is user-signed; later contributions reuse that deposit
// account and only need a confirmation of the amount.
func (s *Service) Contribute(
	ctx context.Context,
	userID uuid.UUID,
	req *UserContributeRequest,
	actor entities.InvestmentActor,
) (*UserContributeResponse, error) {
	if !s.cfg.Enabled {
		return nil, ErrDisabled
	}
	if req == nil || strings.TrimSpace(req.StrategyID) == "" {
		return nil, fmt.Errorf("%w: strategy_id is required", ErrValidationFailed)
	}
	if !req.AmountUSD.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("%w: amount_usd must be greater than zero", ErrValidationFailed)
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
	enrollment, err := s.enrollments.GetByUserAndStrategy(ctx, userID, strategy.ID)
	if err != nil {
		return nil, fmt.Errorf("check enrollment: %w", err)
	}
	if enrollment == nil || enrollment.Status != entities.InvestmentEnrollmentActive {
		return nil, fmt.Errorf("%w: enroll in this strategy before adding money", ErrNotFound)
	}

	limits, err := s.EffectiveLimits(ctx, userID)
	if err != nil {
		return nil, err
	}
	decision, err := s.policy.Evaluate(ctx, PolicyInput{
		UserID:    userID,
		Action:    PolicyActionContribute,
		AmountUSD: req.AmountUSD,
		Risk:      strategy.Risk,
		Limits:    *limits,
	})
	if err != nil {
		return nil, err
	}
	if decision.Verdict == entities.InvestmentVerdictNotSupported ||
		decision.Verdict == entities.InvestmentVerdictRequiresComplianceReview ||
		decision.Verdict == entities.InvestmentVerdictRequiresAuthentication {
		return &UserContributeResponse{Status: entities.InvestmentActionRejected, Policy: decision, Enrollment: enrollment},
			fmt.Errorf("%w: %s", ErrPolicyBlocked, strings.Join(decision.Reasons, "; "))
	}

	source := normaliseFundingSource(req.Source)
	type contributeBinding struct {
		StrategyID string `json:"strategy_id"`
		AmountUSD  string `json:"amount_usd"`
		Source     string `json:"source"`
		Enrollment string `json:"enrollment_id"`
	}
	binding := contributeBinding{
		StrategyID: req.StrategyID,
		AmountUSD:  req.AmountUSD.String(),
		Source:     source,
		Enrollment: enrollment.ID.String(),
	}
	outcome, err := s.confirmMutation(ctx, userID, "fund_enrollment", binding, decision, nil, req.ConfirmationToken)
	if err != nil {
		return nil, err
	}
	if !outcome.Proceed {
		return &UserContributeResponse{
			Status:       entities.InvestmentActionAwaitingConfirmation,
			Enrollment:   enrollment,
			Policy:       decision,
			Confirmation: outcome.Pending,
		}, nil
	}

	// Idempotency: a caller-supplied key makes retries safe. The default is
	// unique per request (never derived from enrollment+amount) so two
	// legitimate top-ups of the same amount can never dedupe each other.
	// Callers that need safe retries must send idempotency_key.
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = "invest-contribute-" + uuid.NewString()
	}
	transfer, err := s.Fund(ctx, userID, enrollment.ID, req.AmountUSD, source, key, actor)
	if err != nil {
		// The portfolio exists but the money did not move. FAILED + non-nil
		// error so handlers answer 5xx with the reason on the body — never
		// a silent COMPLETED the client mistakes for funded.
		return &UserContributeResponse{
			Status:     entities.InvestmentActionFailed,
			Enrollment: enrollment,
			Policy:     decision,
			Funding: &entities.InvestmentFundingTransfer{
				EnrollmentID:  enrollment.ID,
				Direction:     "deposit",
				AmountUSD:     req.AmountUSD,
				Status:        "FAILED",
				FailureReason: err.Error(),
			},
		}, fmt.Errorf("contribution funding failed: %w", err)
	}
	_ = s.SyncEnrollment(ctx, enrollment.ID)
	return &UserContributeResponse{
		Status:     entities.InvestmentActionCompleted,
		Enrollment: enrollment,
		Funding:    transfer,
		Policy:     decision,
	}, nil
}

func gliderStrategyIDOf(strategy *entities.InvestmentStrategy) string {
	if strategy == nil || strategy.GliderStrategyID == nil {
		return ""
	}
	return *strategy.GliderStrategyID
}

// decodeChainIDs recovers the stage-1 chain list from the stored signature
// payload (JSON numbers decode as float64).
func decodeChainIDs(value any) []int {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(list))
	for _, item := range list {
		if f, ok := item.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}

func chainIDsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

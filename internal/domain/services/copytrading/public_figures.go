package copytrading

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// PublicTrade is one publicly disclosed transaction by a public figure,
// normalized from the disclosure dataset.
type PublicTrade struct {
	Ticker          string
	AssetName       string
	Side            string // "buy" or "sell"
	AmountMid       decimal.Decimal
	AmountRange     string
	TransactionDate time.Time
	DisclosureDate  time.Time
	Ref             string // stable dedupe reference
}

// PublicTradesSource provides publicly disclosed trades (congressional
// disclosures etc.). Implemented in infrastructure.
type PublicTradesSource interface {
	GetFigureTrades(ctx context.Context, figureKey string, since time.Time, limit int) ([]PublicTrade, error)
}

const (
	// publicFigureReferenceAUM is the assumed portfolio size used to scale a
	// public figure's disclosed trades down to a drafter's allocation for
	// ongoing copying. Disclosures only give ranges, not portfolio values.
	publicFigureReferenceAUM = 1_000_000

	// publicCopyLookback bounds how far back "recent trades" reach. STOCK Act
	// disclosures can lag the trade by up to 45 days.
	publicCopyLookback = 60 * 24 * time.Hour

	// publicCopyMaxTrades caps how many recent buys an instant copy spreads
	// the user's money across.
	publicCopyMaxTrades = 5

	publicFigureMinDraft = 10
)

// SetPublicTradesSource wires the public disclosure dataset (optional).
func (s *Service) SetPublicTradesSource(src PublicTradesSource) {
	s.publicTrades = src
}

// EnsurePublicConductor returns the conductor row for a public figure,
// creating it on first use. Public conductors have no Rail user.
func (s *Service) EnsurePublicConductor(ctx context.Context, figureKey, displayName, bio string) (*entities.Conductor, error) {
	existing, err := s.repo.GetConductorByExternalKey(ctx, figureKey)
	if err != nil {
		return nil, fmt.Errorf("failed to look up public conductor: %w", err)
	}
	if existing != nil {
		return existing, nil
	}
	now := time.Now().UTC()
	key := figureKey
	conductor := &entities.Conductor{
		ID:             uuid.New(),
		UserID:         nil,
		Source:         entities.ConductorSourcePublic,
		ExternalKey:    &key,
		DisplayName:    displayName,
		Bio:            bio,
		Status:         entities.ConductorStatusActive,
		FeeRate:        decimal.Zero,
		SourceAUM:      decimal.NewFromInt(publicFigureReferenceAUM),
		MinDraftAmount: decimal.NewFromInt(publicFigureMinDraft),
		IsVerified:     true,
		VerifiedAt:     &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.repo.CreateConductor(ctx, conductor); err != nil {
		// Lost a create race: fetch the winner.
		if again, lookupErr := s.repo.GetConductorByExternalKey(ctx, figureKey); lookupErr == nil && again != nil {
			return again, nil
		}
		return nil, fmt.Errorf("failed to create public conductor: %w", err)
	}
	return conductor, nil
}

// CopiedTradeResult reports the outcome of one instantly copied trade.
type CopiedTradeResult struct {
	Ticker        string
	Side          string
	RequestedUSD  decimal.Decimal
	ExecutedUSD   decimal.Decimal
	ExecutedPrice decimal.Decimal
	Status        entities.ExecutionStatus
	Error         string
	DisclosedAt   string
}

// InstantCopyResult is the outcome of CopyRecentTrades.
type InstantCopyResult struct {
	Conductor string
	DraftID   uuid.UUID
	Allocated decimal.Decimal
	Trades    []CopiedTradeResult
}

// CopyRecentTrades executes a public figure's recently disclosed buys for the
// user right now, splitting amount across them proportionally to the disclosed
// trade sizes, and leaves an active draft so future disclosures keep copying.
// Real money: amount is debited up front and orders go to the brokerage.
func (s *Service) CopyRecentTrades(ctx context.Context, userID uuid.UUID, figureKey, displayName string, amount decimal.Decimal) (*InstantCopyResult, error) {
	if s.publicTrades == nil {
		return nil, fmt.Errorf("public trade data source not configured")
	}
	if !amount.IsPositive() {
		return nil, fmt.Errorf("amount must be positive")
	}

	since := time.Now().Add(-publicCopyLookback)
	trades, err := s.publicTrades.GetFigureTrades(ctx, figureKey, since, 50)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch disclosed trades: %w", err)
	}
	buys := make([]PublicTrade, 0, len(trades))
	for _, t := range trades {
		if t.Side == "buy" {
			buys = append(buys, t)
		}
	}
	if len(buys) == 0 {
		return nil, fmt.Errorf("%s has no stock purchases disclosed in the last 60 days", displayName)
	}
	sort.Slice(buys, func(i, j int) bool { return buys[i].AmountMid.GreaterThan(buys[j].AmountMid) })
	if len(buys) > publicCopyMaxTrades {
		buys = buys[:publicCopyMaxTrades]
	}

	conductor, err := s.EnsurePublicConductor(ctx, figureKey, displayName, "")
	if err != nil {
		return nil, err
	}

	// CreateDraft checks and debits the user's balance, and keeps the copy
	// relationship active for future disclosures.
	draft, err := s.CreateDraft(ctx, userID, &entities.CreateDraftRequest{
		ConductorID:      conductor.ID,
		AllocatedCapital: amount,
	})
	if err != nil {
		return nil, err
	}

	batchTotal := decimal.Zero
	for _, b := range buys {
		batchTotal = batchTotal.Add(b.AmountMid)
	}

	result := &InstantCopyResult{
		Conductor: conductor.DisplayName,
		DraftID:   draft.ID,
		Allocated: amount,
	}
	for _, b := range buys {
		tradeResult := CopiedTradeResult{
			Ticker:      b.Ticker,
			Side:        b.Side,
			DisclosedAt: b.DisclosureDate.Format("2006-01-02"),
			// Proportional share of the user's allocation.
			RequestedUSD: amount.Mul(b.AmountMid).Div(batchTotal).RoundBank(2),
		}
		signal, sigErr := s.ensurePublicSignal(ctx, conductor, b, batchTotal)
		if sigErr != nil {
			tradeResult.Status = entities.ExecutionStatusFailed
			tradeResult.Error = sigErr.Error()
			result.Trades = append(result.Trades, tradeResult)
			continue
		}
		if execErr := s.executeCopyTrade(ctx, draft, signal); execErr != nil {
			tradeResult.Status = entities.ExecutionStatusFailed
			tradeResult.Error = execErr.Error()
			result.Trades = append(result.Trades, tradeResult)
			continue
		}
		// executeCopyTrade logs skips (too small / insufficient funds) without
		// erroring; read the log to report what actually happened.
		idempotencyKey := fmt.Sprintf("copy_%s_%s", draft.ID.String(), signal.ID.String())
		if log, logErr := s.repo.GetExecutionLogByIdempotencyKey(ctx, idempotencyKey); logErr == nil && log != nil {
			tradeResult.Status = log.Status
			tradeResult.ExecutedUSD = log.ExecutedValue.RoundBank(2)
			tradeResult.ExecutedPrice = log.ExecutedPrice
			tradeResult.Error = log.ErrorMessage
		} else {
			tradeResult.Status = entities.ExecutionStatusSuccess
		}
		result.Trades = append(result.Trades, tradeResult)
	}

	s.logger.Info("Instant public-figure copy executed",
		zap.String("user_id", userID.String()),
		zap.String("figure", conductor.DisplayName),
		zap.String("amount", amount.String()),
		zap.Int("trades", len(result.Trades)))
	return result, nil
}

// ensurePublicSignal gets or creates the signal row for one disclosed trade.
func (s *Service) ensurePublicSignal(ctx context.Context, conductor *entities.Conductor, trade PublicTrade, batchTotal decimal.Decimal) (*entities.Signal, error) {
	existing, err := s.repo.GetSignalByOrderRef(ctx, conductor.ID, trade.Ref)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	price, err := s.tradingAdapter.GetCurrentPrice(ctx, trade.Ticker)
	if err != nil {
		return nil, fmt.Errorf("no market price for %s: %w", trade.Ticker, err)
	}
	if !price.IsPositive() {
		return nil, fmt.Errorf("no market price for %s", trade.Ticker)
	}
	signalType := entities.SignalTypeBuy
	if trade.Side == "sell" {
		signalType = entities.SignalTypeSell
	}
	aumAtSignal := batchTotal
	if !aumAtSignal.IsPositive() {
		aumAtSignal = decimal.NewFromInt(publicFigureReferenceAUM)
	}
	signal := &entities.Signal{
		ID:                   uuid.New(),
		ConductorID:          conductor.ID,
		AssetTicker:          trade.Ticker,
		AssetName:            trade.AssetName,
		SignalType:           signalType,
		Side:                 trade.Side,
		BaseQuantity:         trade.AmountMid.Div(price),
		BasePrice:            price,
		BaseValue:            trade.AmountMid,
		ConductorAUMAtSignal: aumAtSignal,
		OrderID:              trade.Ref,
		Status:               entities.SignalStatusPending,
		CreatedAt:            time.Now().UTC(),
	}
	if err := s.repo.CreateSignal(ctx, signal); err != nil {
		return nil, fmt.Errorf("failed to create signal: %w", err)
	}
	return signal, nil
}

// IngestPublicDisclosures creates pending signals for new disclosures of every
// public figure that users actively copy. The copy trading worker then
// replicates them into drafter accounts. Called on a schedule by the public
// trades worker.
func (s *Service) IngestPublicDisclosures(ctx context.Context) error {
	if s.publicTrades == nil {
		return nil
	}
	conductors, err := s.repo.GetActivePublicConductorsWithDrafts(ctx)
	if err != nil {
		return fmt.Errorf("failed to list public conductors: %w", err)
	}
	since := time.Now().Add(-publicCopyLookback)
	for _, conductor := range conductors {
		if conductor.ExternalKey == nil {
			continue
		}
		trades, err := s.publicTrades.GetFigureTrades(ctx, *conductor.ExternalKey, since, 50)
		if err != nil {
			s.logger.Warn("failed to fetch disclosures for public conductor",
				zap.String("conductor", conductor.DisplayName), zap.Error(err))
			continue
		}
		for _, trade := range trades {
			exists, err := s.repo.SignalExistsByOrderRef(ctx, conductor.ID, trade.Ref)
			if err != nil || exists {
				continue
			}
			// Ongoing copies scale against the reference AUM, not a batch.
			if _, err := s.ensurePublicSignal(ctx, conductor, trade, decimal.NewFromInt(publicFigureReferenceAUM)); err != nil {
				s.logger.Warn("failed to create signal from disclosure",
					zap.String("conductor", conductor.DisplayName),
					zap.String("ticker", trade.Ticker),
					zap.Error(err))
			}
		}
	}
	return nil
}

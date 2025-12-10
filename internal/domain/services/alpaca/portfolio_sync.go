package alpaca

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	alpacaAdapter "github.com/rail-service/rail_service/internal/adapters/alpaca"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"go.uber.org/zap"
)

// PortfolioSyncService handles synchronization between local and Alpaca portfolios
type PortfolioSyncService struct {
	alpacaClient *alpacaAdapter.Client
	accountRepo  AccountRepository
	positionRepo PositionRepository
	balanceRepo  BalanceRepository
	logger       *zap.Logger
}

func NewPortfolioSyncService(
	alpacaClient *alpacaAdapter.Client,
	accountRepo AccountRepository,
	positionRepo PositionRepository,
	balanceRepo BalanceRepository,
	logger *zap.Logger,
) *PortfolioSyncService {
	return &PortfolioSyncService{
		alpacaClient: alpacaClient,
		accountRepo:  accountRepo,
		positionRepo: positionRepo,
		balanceRepo:  balanceRepo,
		logger:       logger,
	}
}

// SyncPositions synchronizes local positions with Alpaca
func (s *PortfolioSyncService) SyncPositions(ctx context.Context, userID uuid.UUID) error {
	account, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return fmt.Errorf("no Alpaca account found for user")
	}

	// Fetch positions from Alpaca
	alpacaPositions, err := s.alpacaClient.ListPositions(ctx, account.AlpacaAccountID)
	if err != nil {
		return fmt.Errorf("list Alpaca positions: %w", err)
	}

	now := time.Now()

	// Get local positions
	localPositions, err := s.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("get local positions: %w", err)
	}

	// Build map of local positions by symbol
	localMap := make(map[string]*entities.InvestmentPosition)
	for _, pos := range localPositions {
		localMap[pos.Symbol] = pos
	}

	// Track which symbols we've seen from Alpaca
	seenSymbols := make(map[string]bool)

	// Update/create positions from Alpaca
	for _, ap := range alpacaPositions {
		seenSymbols[ap.Symbol] = true

		pos := &entities.InvestmentPosition{
			UserID:          userID,
			AlpacaAccountID: &account.ID,
			Symbol:          ap.Symbol,
			AssetID:         ap.AssetID,
			Qty:             ap.Qty,
			QtyAvailable:    ap.QtyAvailable,
			AvgEntryPrice:   ap.AvgEntryPrice,
			MarketValue:     ap.MarketValue,
			CostBasis:       ap.CostBasis,
			UnrealizedPL:    ap.UnrealizedPL,
			UnrealizedPLPC:  ap.UnrealizedPLPC,
			CurrentPrice:    ap.CurrentPrice,
			LastdayPrice:    ap.LastdayPrice,
			ChangeToday:     ap.ChangeToday,
			Side:            ap.Side,
			LastSyncedAt:    &now,
			UpdatedAt:       now,
		}

		if existing, ok := localMap[ap.Symbol]; ok {
			pos.ID = existing.ID
			pos.CreatedAt = existing.CreatedAt
		} else {
			pos.ID = uuid.New()
			pos.CreatedAt = now
		}

		if err := s.positionRepo.Upsert(ctx, pos); err != nil {
			s.logger.Error("Failed to upsert position", zap.String("symbol", ap.Symbol), zap.Error(err))
		}
	}

	// Remove positions that no longer exist in Alpaca
	for symbol := range localMap {
		if !seenSymbols[symbol] {
			if err := s.positionRepo.DeleteByUserAndSymbol(ctx, userID, symbol); err != nil {
				s.logger.Error("Failed to delete stale position", zap.String("symbol", symbol), zap.Error(err))
			}
		}
	}

	s.logger.Info("Positions synced",
		zap.String("user_id", userID.String()),
		zap.Int("alpaca_positions", len(alpacaPositions)),
		zap.Int("local_positions", len(localPositions)))

	return nil
}

// ReconcilePortfolio compares local and Alpaca data and returns discrepancies
func (s *PortfolioSyncService) ReconcilePortfolio(ctx context.Context, userID uuid.UUID) (*entities.AlpacaReconciliationReport, error) {
	account, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("no Alpaca account found")
	}

	// Fetch Alpaca data
	alpacaAccount, err := s.alpacaClient.GetAccount(ctx, account.AlpacaAccountID)
	if err != nil {
		return nil, fmt.Errorf("get Alpaca account: %w", err)
	}

	alpacaPositions, err := s.alpacaClient.ListPositions(ctx, account.AlpacaAccountID)
	if err != nil {
		return nil, fmt.Errorf("list Alpaca positions: %w", err)
	}

	// Get local data
	localPositions, err := s.positionRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get local positions: %w", err)
	}

	localBalance, err := s.balanceRepo.Get(ctx, userID)
	if err != nil {
		s.logger.Warn("Failed to get local balance", zap.Error(err))
	}

	report := &entities.AlpacaReconciliationReport{
		UserID:          userID,
		AlpacaAccountID: account.AlpacaAccountID,
		ReconciledAt:    time.Now(),
	}

	// Build maps for comparison
	localMap := make(map[string]*entities.InvestmentPosition)
	for _, pos := range localPositions {
		localMap[pos.Symbol] = pos
	}

	alpacaMap := make(map[string]entities.AlpacaPositionResponse)
	for _, pos := range alpacaPositions {
		alpacaMap[pos.Symbol] = pos
	}

	// Compare positions
	for symbol, alpacaPos := range alpacaMap {
		if localPos, ok := localMap[symbol]; ok {
			report.PositionsMatched++
			// Check for quantity discrepancy
			diff := localPos.Qty.Sub(alpacaPos.Qty)
			if !diff.IsZero() {
				report.Discrepancies = append(report.Discrepancies, entities.PositionDiscrepancy{
					Symbol:     symbol,
					LocalQty:   localPos.Qty,
					AlpacaQty:  alpacaPos.Qty,
					Difference: diff,
				})
			}
		} else {
			report.PositionsAdded++
			report.Discrepancies = append(report.Discrepancies, entities.PositionDiscrepancy{
				Symbol:     symbol,
				LocalQty:   decimal.Zero,
				AlpacaQty:  alpacaPos.Qty,
				Difference: alpacaPos.Qty.Neg(),
			})
		}
	}

	// Check for positions in local but not in Alpaca
	for symbol, localPos := range localMap {
		if _, ok := alpacaMap[symbol]; !ok {
			report.PositionsRemoved++
			report.Discrepancies = append(report.Discrepancies, entities.PositionDiscrepancy{
				Symbol:     symbol,
				LocalQty:   localPos.Qty,
				AlpacaQty:  decimal.Zero,
				Difference: localPos.Qty,
			})
		}
	}

	// Check balance discrepancy
	if localBalance != nil {
		if !localBalance.BuyingPower.Equal(alpacaAccount.BuyingPower) {
			report.BalanceDiscrepancy = &entities.BalanceDiscrepancy{
				LocalBuyingPower:  localBalance.BuyingPower,
				AlpacaBuyingPower: alpacaAccount.BuyingPower,
				LocalCash:         decimal.Zero, // We don't track cash separately
				AlpacaCash:        alpacaAccount.Cash,
			}
		}
	}

	s.logger.Info("Portfolio reconciliation complete",
		zap.String("user_id", userID.String()),
		zap.Int("matched", report.PositionsMatched),
		zap.Int("discrepancies", len(report.Discrepancies)))

	return report, nil
}

// UpdateMarketValues updates market values for all positions
func (s *PortfolioSyncService) UpdateMarketValues(ctx context.Context) error {
	// This would typically be called periodically to update all positions
	// For now, we sync per-user when they request portfolio data
	s.logger.Info("Market value update triggered")
	return nil
}

// SyncAllAccounts syncs positions for all active accounts
func (s *PortfolioSyncService) SyncAllAccounts(ctx context.Context) error {
	// This would iterate through all accounts and sync
	// Implementation depends on having a method to list all accounts
	s.logger.Info("Full account sync triggered")
	return nil
}

// GetPortfolioSummary returns a summary of user's portfolio from Alpaca
func (s *PortfolioSyncService) GetPortfolioSummary(ctx context.Context, userID uuid.UUID) (*PortfolioSummary, error) {
	account, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if account == nil {
		return nil, nil
	}

	// Fetch fresh data from Alpaca
	alpacaAccount, err := s.alpacaClient.GetAccount(ctx, account.AlpacaAccountID)
	if err != nil {
		return nil, fmt.Errorf("get Alpaca account: %w", err)
	}

	positions, err := s.alpacaClient.ListPositions(ctx, account.AlpacaAccountID)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}

	// Calculate totals
	totalMarketValue := decimal.Zero
	totalUnrealizedPL := decimal.Zero
	totalCostBasis := decimal.Zero

	for _, pos := range positions {
		totalMarketValue = totalMarketValue.Add(pos.MarketValue)
		totalUnrealizedPL = totalUnrealizedPL.Add(pos.UnrealizedPL)
		totalCostBasis = totalCostBasis.Add(pos.CostBasis)
	}

	return &PortfolioSummary{
		BuyingPower:     alpacaAccount.BuyingPower,
		Cash:            alpacaAccount.Cash,
		PortfolioValue:  alpacaAccount.PortfolioValue,
		Equity:          alpacaAccount.Equity,
		MarketValue:     totalMarketValue,
		UnrealizedPL:    totalUnrealizedPL,
		CostBasis:       totalCostBasis,
		PositionCount:   len(positions),
		TradingBlocked:  alpacaAccount.TradingBlocked,
		TransfersBlocked: alpacaAccount.TransfersBlocked,
	}, nil
}

// PortfolioSummary represents a summary of portfolio data
type PortfolioSummary struct {
	BuyingPower      decimal.Decimal `json:"buying_power"`
	Cash             decimal.Decimal `json:"cash"`
	PortfolioValue   decimal.Decimal `json:"portfolio_value"`
	Equity           decimal.Decimal `json:"equity"`
	MarketValue      decimal.Decimal `json:"market_value"`
	UnrealizedPL     decimal.Decimal `json:"unrealized_pl"`
	CostBasis        decimal.Decimal `json:"cost_basis"`
	PositionCount    int             `json:"position_count"`
	TradingBlocked   bool            `json:"trading_blocked"`
	TransfersBlocked bool            `json:"transfers_blocked"`
}

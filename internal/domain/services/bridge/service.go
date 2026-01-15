package bridge

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/cctp"
	"go.uber.org/zap"
)

// BridgeRepository defines persistence operations for bridge transactions
type BridgeRepository interface {
	Create(ctx context.Context, bridge *entities.BridgeTransaction) error
	GetByID(ctx context.Context, id uuid.UUID) (*entities.BridgeTransaction, error)
	GetBySourceTxHash(ctx context.Context, txHash string) (*entities.BridgeTransaction, error)
	GetPendingBridges(ctx context.Context) ([]*entities.BridgeTransaction, error)
	GetByStatus(ctx context.Context, status entities.BridgeStatus) ([]*entities.BridgeTransaction, error)
	Update(ctx context.Context, bridge *entities.BridgeTransaction) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status entities.BridgeStatus, errorMsg string) error
}

// WalletService provides wallet operations
type WalletService interface {
	GetUserWalletByChain(ctx context.Context, userID uuid.UUID, chain string) (*entities.ManagedWallet, error)
}

// DepositProcessor handles deposit processing after bridge completion
type DepositProcessor interface {
	ProcessBridgeDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, txHash string, bridgeID uuid.UUID) error
}

// Service orchestrates Polygon → Solana USDC transfers via CCTP
type Service struct {
	cctpClient       cctp.CCTPClient
	bridgeRepo       BridgeRepository
	walletService    WalletService
	depositProcessor DepositProcessor
	logger           *zap.Logger
}

// NewService creates a new bridge service
func NewService(
	cctpClient cctp.CCTPClient,
	bridgeRepo BridgeRepository,
	walletService WalletService,
	depositProcessor DepositProcessor,
	logger *zap.Logger,
) *Service {
	return &Service{
		cctpClient:       cctpClient,
		bridgeRepo:       bridgeRepo,
		walletService:    walletService,
		depositProcessor: depositProcessor,
		logger:           logger,
	}
}

// InitiateBridge records a bridge intent when a Polygon deposit is detected
func (s *Service) InitiateBridge(ctx context.Context, req *entities.BridgeRequest) (*entities.BridgeTransaction, error) {
	bridge := &entities.BridgeTransaction{
		ID:          uuid.New(),
		UserID:      req.UserID,
		SourceChain: req.SourceChain,
		DestChain:   "SOL",
		Amount:      req.Amount,
		DestAddress: req.DestAddress,
		Status:      entities.BridgeStatusPending,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.bridgeRepo.Create(ctx, bridge); err != nil {
		return nil, fmt.Errorf("create bridge: %w", err)
	}

	s.logger.Info("Bridge initiated",
		zap.String("bridge_id", bridge.ID.String()),
		zap.String("user_id", req.UserID.String()),
		zap.String("source_chain", req.SourceChain),
		zap.String("amount", req.Amount.String()))

	return bridge, nil
}

// RecordBurnTx updates the bridge with the burn transaction hash
func (s *Service) RecordBurnTx(ctx context.Context, bridgeID uuid.UUID, txHash string) error {
	bridge, err := s.bridgeRepo.GetByID(ctx, bridgeID)
	if err != nil {
		return err
	}

	bridge.SourceTxHash = txHash
	bridge.Status = entities.BridgeStatusBurning
	if err := s.bridgeRepo.Update(ctx, bridge); err != nil {
		return fmt.Errorf("update bridge: %w", err)
	}

	s.logger.Info("Burn tx recorded",
		zap.String("bridge_id", bridgeID.String()),
		zap.String("tx_hash", txHash))

	return nil
}

// PollAttestation checks for attestation and returns true if complete
func (s *Service) PollAttestation(ctx context.Context, bridgeID uuid.UUID) (bool, error) {
	bridge, err := s.bridgeRepo.GetByID(ctx, bridgeID)
	if err != nil {
		return false, err
	}

	if bridge.SourceTxHash == "" {
		return false, fmt.Errorf("no source tx hash for bridge %s", bridgeID)
	}

	attestation, err := s.cctpClient.GetAttestation(ctx, bridge.SourceTxHash)
	if err != nil {
		if err == cctp.ErrNoMessages {
			return false, nil // Not ready yet
		}
		return false, fmt.Errorf("get attestation: %w", err)
	}

	if len(attestation.Messages) == 0 {
		return false, nil
	}

	msg := attestation.Messages[0]
	if msg.AttestationStatus != cctp.AttestationStatusComplete {
		// Update status to attesting if not already
		if bridge.Status != entities.BridgeStatusAttesting {
			bridge.Status = entities.BridgeStatusAttesting
			s.bridgeRepo.Update(ctx, bridge)
		}
		return false, nil
	}

	// Attestation complete - store it
	bridge.MessageHash = msg.MessageHash
	bridge.Attestation = msg.Attestation
	bridge.Status = entities.BridgeStatusMinting
	if err := s.bridgeRepo.Update(ctx, bridge); err != nil {
		return false, fmt.Errorf("update bridge attestation: %w", err)
	}

	s.logger.Info("Attestation received",
		zap.String("bridge_id", bridgeID.String()),
		zap.String("message_hash", msg.MessageHash))

	return true, nil
}

// CompleteBridge finalizes the bridge after mint is executed on Solana
func (s *Service) CompleteBridge(ctx context.Context, bridgeID uuid.UUID, destTxHash string) error {
	bridge, err := s.bridgeRepo.GetByID(ctx, bridgeID)
	if err != nil {
		return err
	}

	bridge.DestTxHash = destTxHash
	bridge.Status = entities.BridgeStatusCompleted
	if err := s.bridgeRepo.Update(ctx, bridge); err != nil {
		return fmt.Errorf("update bridge: %w", err)
	}

	// Trigger deposit processing (70/30 allocation)
	if s.depositProcessor != nil {
		if err := s.depositProcessor.ProcessBridgeDeposit(ctx, bridge.UserID, bridge.Amount, destTxHash, bridge.ID); err != nil {
			s.logger.Error("Failed to process bridge deposit",
				zap.String("bridge_id", bridgeID.String()),
				zap.Error(err))
			// Don't fail the bridge completion for this
		}
	}

	s.logger.Info("Bridge completed",
		zap.String("bridge_id", bridgeID.String()),
		zap.String("dest_tx_hash", destTxHash))

	return nil
}

// FailBridge marks a bridge as failed with an error message
func (s *Service) FailBridge(ctx context.Context, bridgeID uuid.UUID, errorMsg string) error {
	s.logger.Error("Bridge failed",
		zap.String("bridge_id", bridgeID.String()),
		zap.String("error", errorMsg))
	return s.bridgeRepo.UpdateStatus(ctx, bridgeID, entities.BridgeStatusFailed, errorMsg)
}

// GetBridge retrieves a bridge by ID
func (s *Service) GetBridge(ctx context.Context, bridgeID uuid.UUID) (*entities.BridgeTransaction, error) {
	return s.bridgeRepo.GetByID(ctx, bridgeID)
}

// GetPendingBridges retrieves all bridges that need processing
func (s *Service) GetPendingBridges(ctx context.Context) ([]*entities.BridgeTransaction, error) {
	return s.bridgeRepo.GetPendingBridges(ctx)
}

// GetBridgesByStatus retrieves bridges by status
func (s *Service) GetBridgesByStatus(ctx context.Context, status entities.BridgeStatus) ([]*entities.BridgeTransaction, error) {
	return s.bridgeRepo.GetByStatus(ctx, status)
}

package di

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

// complianceUserFreezer implements compliance.UserFreezer by deactivating the user account.
type complianceUserFreezer struct {
	userRepo *repositories.UserRepository
	logger   *zap.Logger
}

func (f *complianceUserFreezer) FreezeUser(ctx context.Context, userID uuid.UUID, reason string) error {
	f.logger.Warn("Freezing user account due to compliance violation",
		zap.String("user_id", userID.String()),
		zap.String("reason", reason))

	if err := f.userRepo.DeactivateUser(ctx, userID); err != nil {
		return fmt.Errorf("freeze user: %w", err)
	}
	return nil
}

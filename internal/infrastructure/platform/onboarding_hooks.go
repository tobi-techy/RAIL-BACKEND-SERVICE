package platform

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// BabyStepsSeeder is the minimal surface the platform package needs to fire
// the 7-step goal ladder for a freshly-linked user. Satisfied by
// goals.BabyStepsSeed. Nil-safe at every call site.
type BabyStepsSeeder interface {
	Seed(ctx context.Context, userID uuid.UUID) (int, error)
}

// SeedBabyStepsOnLink fires the seeder asynchronously with panic recovery so
// a failure on the onboarding path can never block or break a link
// confirmation. Returns immediately; the seed runs in its own goroutine with a
// short bounded context so a stuck database can't keep the goroutine alive.
//
// Safe to call with a nil seeder or nil user id (no-op).
func SeedBabyStepsOnLink(seeder BabyStepsSeeder, userID uuid.UUID, logger *zap.Logger) {
	if seeder == nil || userID == uuid.Nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				if logger != nil {
					logger.Warn("baby steps seed panicked; recovered",
						zap.Stringer("user_id", userID),
						zap.Any("panic", r),
					)
				}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		created, err := seeder.Seed(ctx, userID)
		if err != nil {
			if logger != nil {
				logger.Warn("baby steps seed failed during onboarding",
					zap.Stringer("user_id", userID), zap.Error(err))
			}
			return
		}
		if logger != nil && created > 0 {
			logger.Info("baby steps seeded for new user",
				zap.Stringer("user_id", userID),
				zap.Int("created", created),
			)
		}
	}()
}

package ai

import (
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai/core"
	aiexecution "github.com/rail-service/rail_service/internal/domain/services/ai/execution"
	aiprompttools "github.com/rail-service/rail_service/internal/domain/services/ai/prompt/tools"
	aivoicelimits "github.com/rail-service/rail_service/internal/domain/services/ai/voice/limits"
	"context"
	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"go.uber.org/zap"
)

// Type aliases preserve backward compatibility after moving interfaces to core.
type VoiceDailyLimiterer = aiservice.VoiceDailyLimiterer
type MerchantEnricher = aiservice.MerchantEnricher

// Execution types moved to execution subpackage.
var ErrStepUpRequired = aiexecution.ErrStepUpRequired

func WithStepUpToken(ctx context.Context, token string) context.Context {
	return aiexecution.WithStepUpToken(ctx, token)
}

type QualityVerdict = aiexecution.QualityVerdict

func CheckResponseQuality(response string) QualityVerdict {
	return aiexecution.CheckResponseQuality(response)
}

func QualityCorrectionHint(failures []string) string {
	return aiexecution.QualityCorrectionHint(failures)
}

func GuardResponse(content, grounding, anomalies string) string {
	return aiexecution.GuardResponse(content, grounding, anomalies)
}

// Voice types moved to voice/limits subpackage.
type VoiceSessionRateLimiter = aivoicelimits.VoiceSessionRateLimiter

func NewVoiceDailyLimiter(redis cache.RedisClient, limitUSD float64) *aivoicelimits.VoiceDailyLimiter {
	return aivoicelimits.NewVoiceDailyLimiter(redis, limitUSD)
}

func NewVoiceSessionRateLimiter(redis cache.RedisClient, maxPerHour int, logger *zap.Logger) *aivoicelimits.VoiceSessionRateLimiter {
	return aivoicelimits.NewVoiceSessionRateLimiter(redis, maxPerHour, logger)
}

// Pending actions - NewRedisPendingActions moved to execution subpackage.
func NewRedisPendingActions(redis cache.RedisClient, logger *zap.Logger) PendingActionStore {
	return aiexecution.NewRedisPendingActions(redis, logger)
}

// Prompt tools moved to prompt/tools subpackage.
const SystemPromptTools = aiprompttools.SystemPromptTools

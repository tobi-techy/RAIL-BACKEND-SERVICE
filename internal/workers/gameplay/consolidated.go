package gameplay

import (
	"context"
	"time"

	"go.uber.org/zap"
)

type Worker struct {
	streakEvaluator    *StreakEvaluator
	challengeRotator   *ChallengeRotator
	achievementChecker *AchievementChecker
	insightGenerator   *InsightGenerator
	dailyMetrics       *DailyMetricsWorker
	logger             *zap.Logger
	interval           time.Duration
}

func NewWorker(
	streakEvaluator *StreakEvaluator,
	challengeRotator *ChallengeRotator,
	achievementChecker *AchievementChecker,
	insightGenerator *InsightGenerator,
	dailyMetrics *DailyMetricsWorker,
	logger *zap.Logger,
) *Worker {
	return &Worker{
		streakEvaluator:    streakEvaluator,
		challengeRotator:   challengeRotator,
		achievementChecker: achievementChecker,
		insightGenerator:   insightGenerator,
		dailyMetrics:       dailyMetrics,
		logger:             logger,
		interval:           1 * time.Hour,
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Gameplay worker started")
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.runAll(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Gameplay worker stopped")
			return
		case <-ticker.C:
			w.runAll(ctx)
		}
	}
}

func (w *Worker) runAll(ctx context.Context) {
	if w.streakEvaluator != nil {
		w.streakEvaluator.EvaluateStreaks(ctx)
	}
	if w.challengeRotator != nil {
		w.challengeRotator.RotateChallenges(ctx)
	}
	if w.achievementChecker != nil {
		w.achievementChecker.CheckAchievements(ctx)
	}
	if w.insightGenerator != nil {
		w.insightGenerator.GenerateInsights(ctx)
	}
	if w.dailyMetrics != nil {
		w.dailyMetrics.ComputeDailyMetrics(ctx)
	}
}

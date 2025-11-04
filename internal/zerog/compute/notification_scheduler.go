package compute

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/stack-service/stack_service/internal/domain/entities"
	"github.com/stack-service/stack_service/internal/domain/services"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// NotificationScheduler manages scheduled notifications for AI summaries and updates
type NotificationScheduler struct {
	aicfoService        *services.AICfoService
	notificationService *services.NotificationService
	newsService         NewsAggregator
	userRepo            UserRepository
	scheduler           *cron.Cron
	logger              *zap.Logger
	tracer              trace.Tracer
	metrics             *SchedulerMetrics
	config              *SchedulerConfig
	running             bool
}

// SchedulerConfig contains configuration for the notification scheduler
type SchedulerConfig struct {
	DailyNewsSummaryTime     string // Cron expression: "0 8 * * *" = 8 AM daily
	WeeklySummaryTime        string // Cron expression: "0 7 * * 1" = 7 AM Monday
	MarketOpenNotifications  bool
	EnableDailyDigest        bool
	EnableWeeklySummary      bool
	EnablePerformanceAlerts  bool
	BatchSize                int
	ConcurrencyLimit         int
	RetryAttempts            int
	RetryDelay               time.Duration
}

// SchedulerMetrics contains observability metrics
type SchedulerMetrics struct {
	ScheduledJobsExecuted metric.Int64Counter
	JobExecutionDuration  metric.Float64Histogram
	JobFailures           metric.Int64Counter
	UsersProcessed        metric.Int64Counter
	NotificationsSent     metric.Int64Counter
}

// UserRepository interface for user data access
type UserRepository interface {
	GetAllActiveUsers(ctx context.Context) ([]uuid.UUID, error)
	GetUserNotificationSettings(ctx context.Context, userID uuid.UUID) (*NotificationSettings, error)
	GetUserEmail(ctx context.Context, userID uuid.UUID) (string, error)
	GetUserDeviceTokens(ctx context.Context, userID uuid.UUID) ([]DeviceToken, error)
}

// NotificationSettings represents user notification preferences
type NotificationSettings struct {
	DailyDigest         bool      `json:"daily_digest"`
	WeeklySummary       bool      `json:"weekly_summary"`
	PerformanceAlerts   bool      `json:"performance_alerts"`
	MarketNews          bool      `json:"market_news"`
	PushEnabled         bool      `json:"push_enabled"`
	EmailEnabled        bool      `json:"email_enabled"`
	QuietHoursStart     time.Time `json:"quiet_hours_start"`
	QuietHoursEnd       time.Time `json:"quiet_hours_end"`
	Timezone            string    `json:"timezone"`
	PreferredLanguage   string    `json:"preferred_language"`
}

// DeviceToken represents a user's device for push notifications
type DeviceToken struct {
	Token    string    `json:"token"`
	Platform string    `json:"platform"` // ios, android
	Active   bool      `json:"active"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewsAggregator interface for market news aggregation
type NewsAggregator interface {
	GetDailyMarketNews(ctx context.Context, date time.Time) (*MarketNews, error)
	GetPortfolioRelevantNews(ctx context.Context, userID uuid.UUID, date time.Time) (*PersonalizedNews, error)
}

// MarketNews represents aggregated market news
type MarketNews struct {
	Date       time.Time   `json:"date"`
	Headlines  []NewsItem  `json:"headlines"`
	MarketData MarketData  `json:"market_data"`
	Summary    string      `json:"summary"`
}

// NewsItem represents a single news article
type NewsItem struct {
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Source      string    `json:"source"`
	PublishedAt time.Time `json:"published_at"`
	URL         string    `json:"url"`
	Sentiment   string    `json:"sentiment"` // positive, negative, neutral
	Relevance   float64   `json:"relevance"`
}

// MarketData represents daily market statistics
type MarketData struct {
	SP500Change      float64 `json:"sp500_change"`
	NasdaqChange     float64 `json:"nasdaq_change"`
	DowChange        float64 `json:"dow_change"`
	VIX              float64 `json:"vix"`
	TreasuryYield    float64 `json:"treasury_yield"`
	TopGainers       []string `json:"top_gainers"`
	TopLosers        []string `json:"top_losers"`
}

// PersonalizedNews represents news relevant to a user's portfolio
type PersonalizedNews struct {
	UserID          uuid.UUID  `json:"user_id"`
	Date            time.Time  `json:"date"`
	PortfolioNews   []NewsItem `json:"portfolio_news"`
	SectorNews      []NewsItem `json:"sector_news"`
	GeneralNews     []NewsItem `json:"general_news"`
	AISummary       string     `json:"ai_summary"`
}

// DailyDigestData contains data for daily digest notifications
type DailyDigestData struct {
	UserID              uuid.UUID
	Date                time.Time
	PortfolioSummary    *PortfolioSnapshot
	News                *PersonalizedNews
	PerformanceHighlight string
	ActionableInsights  []string
}

// PortfolioSnapshot represents a point-in-time portfolio snapshot
type PortfolioSnapshot struct {
	TotalValue     float64   `json:"total_value"`
	DayChange      float64   `json:"day_change"`
	DayChangePct   float64   `json:"day_change_pct"`
	TopPerformer   string    `json:"top_performer"`
	WorstPerformer string    `json:"worst_performer"`
	Timestamp      time.Time `json:"timestamp"`
}

// NewNotificationScheduler creates a new notification scheduler
func NewNotificationScheduler(
	aicfoService *services.AICfoService,
	notificationService *services.NotificationService,
	newsService NewsAggregator,
	userRepo UserRepository,
	config *SchedulerConfig,
	logger *zap.Logger,
) (*NotificationScheduler, error) {
	
	tracer := otel.Tracer("notification-scheduler")
	meter := otel.Meter("notification-scheduler")
	
	metrics, err := initSchedulerMetrics(meter)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	scheduler := cron.New(cron.WithSeconds(), cron.WithLogger(cron.VerbosePrintfLogger(logger.Sugar())))

	return &NotificationScheduler{
		aicfoService:        aicfoService,
		notificationService: notificationService,
		newsService:         newsService,
		userRepo:            userRepo,
		scheduler:           scheduler,
		logger:              logger,
		tracer:              tracer,
		metrics:             metrics,
		config:              config,
		running:             false,
	}, nil
}

// Start begins the notification scheduler
func (ns *NotificationScheduler) Start(ctx context.Context) error {
	if ns.running {
		return fmt.Errorf("scheduler already running")
	}

	ns.logger.Info("Starting notification scheduler",
		zap.String("daily_news_time", ns.config.DailyNewsSummaryTime),
		zap.String("weekly_summary_time", ns.config.WeeklySummaryTime),
		zap.Bool("daily_digest", ns.config.EnableDailyDigest),
		zap.Bool("weekly_summary", ns.config.EnableWeeklySummary),
	)

	// Schedule daily news digest
	if ns.config.EnableDailyDigest {
		_, err := ns.scheduler.AddFunc(ns.config.DailyNewsSummaryTime, func() {
			if err := ns.runDailyDigest(ctx); err != nil {
				ns.logger.Error("Daily digest job failed", zap.Error(err))
			}
		})
		if err != nil {
			return fmt.Errorf("failed to schedule daily digest: %w", err)
		}
		ns.logger.Info("Scheduled daily news digest", zap.String("schedule", ns.config.DailyNewsSummaryTime))
	}

	// Schedule weekly portfolio summary
	if ns.config.EnableWeeklySummary {
		_, err := ns.scheduler.AddFunc(ns.config.WeeklySummaryTime, func() {
			if err := ns.runWeeklySummary(ctx); err != nil {
				ns.logger.Error("Weekly summary job failed", zap.Error(err))
			}
		})
		if err != nil {
			return fmt.Errorf("failed to schedule weekly summary: %w", err)
		}
		ns.logger.Info("Scheduled weekly portfolio summary", zap.String("schedule", ns.config.WeeklySummaryTime))
	}

	// Schedule performance alerts check (every hour during market hours)
	if ns.config.EnablePerformanceAlerts {
		_, err := ns.scheduler.AddFunc("0 * 9-16 * * 1-5", func() {
			if err := ns.checkPerformanceAlerts(ctx); err != nil {
				ns.logger.Error("Performance alerts check failed", zap.Error(err))
			}
		})
		if err != nil {
			return fmt.Errorf("failed to schedule performance alerts: %w", err)
		}
		ns.logger.Info("Scheduled performance alerts check")
	}

	ns.scheduler.Start()
	ns.running = true
	
	ns.logger.Info("Notification scheduler started successfully",
		zap.Int("jobs_scheduled", len(ns.scheduler.Entries())),
	)

	return nil
}

// Stop gracefully stops the scheduler
func (ns *NotificationScheduler) Stop(ctx context.Context) error {
	if !ns.running {
		return nil
	}

	ns.logger.Info("Stopping notification scheduler")
	
	stopCtx := ns.scheduler.Stop()
	<-stopCtx.Done()
	
	ns.running = false
	ns.logger.Info("Notification scheduler stopped")
	
	return nil
}

// runDailyDigest processes and sends daily news digests to all users
func (ns *NotificationScheduler) runDailyDigest(ctx context.Context) error {
	startTime := time.Now()
	ctx, span := ns.tracer.Start(ctx, "scheduler.run_daily_digest")
	defer span.End()

	ns.logger.Info("Starting daily digest job")

	// Get all active users
	userIDs, err := ns.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get active users: %w", err)
	}

	ns.logger.Info("Processing daily digest",
		zap.Int("user_count", len(userIDs)),
	)

	// Process users in batches with concurrency control
	successCount := 0
	failureCount := 0
	
	for i := 0; i < len(userIDs); i += ns.config.BatchSize {
		end := i + ns.config.BatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		
		batch := userIDs[i:end]
		
		for _, userID := range batch {
			if err := ns.processDailyDigestForUser(ctx, userID); err != nil {
				ns.logger.Warn("Failed to process daily digest for user",
					zap.String("user_id", userID.String()),
					zap.Error(err),
				)
				failureCount++
			} else {
				successCount++
			}
		}
	}

	duration := time.Since(startTime)
	
	// Record metrics
	ns.metrics.ScheduledJobsExecuted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("job_type", "daily_digest"),
	))
	ns.metrics.JobExecutionDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("job_type", "daily_digest"),
	))
	ns.metrics.UsersProcessed.Add(ctx, int64(successCount), metric.WithAttributes(
		attribute.String("job_type", "daily_digest"),
	))
	
	if failureCount > 0 {
		ns.metrics.JobFailures.Add(ctx, int64(failureCount), metric.WithAttributes(
			attribute.String("job_type", "daily_digest"),
		))
	}

	ns.logger.Info("Daily digest job completed",
		zap.Int("success_count", successCount),
		zap.Int("failure_count", failureCount),
		zap.Duration("duration", duration),
	)

	return nil
}

// processDailyDigestForUser processes daily digest for a single user
func (ns *NotificationScheduler) processDailyDigestForUser(ctx context.Context, userID uuid.UUID) error {
	ctx, span := ns.tracer.Start(ctx, "scheduler.process_daily_digest_user", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	// Check user notification settings
	settings, err := ns.userRepo.GetUserNotificationSettings(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get notification settings: %w", err)
	}

	if !settings.DailyDigest {
		ns.logger.Debug("Daily digest disabled for user", zap.String("user_id", userID.String()))
		return nil
	}

	// Check quiet hours
	if ns.isQuietHours(settings) {
		ns.logger.Debug("User in quiet hours, skipping", zap.String("user_id", userID.String()))
		return nil
	}

	// Get personalized news
	news, err := ns.newsService.GetPortfolioRelevantNews(ctx, userID, time.Now())
	if err != nil {
		return fmt.Errorf("failed to get news: %w", err)
	}

	// Get portfolio snapshot (simplified - would normally fetch from portfolio service)
	portfolioSnapshot := &PortfolioSnapshot{
		Timestamp: time.Now(),
		// Would be populated from actual portfolio data
	}

	// Build digest data
	digestData := &DailyDigestData{
		UserID:           userID,
		Date:             time.Now(),
		PortfolioSummary: portfolioSnapshot,
		News:             news,
		ActionableInsights: []string{
			// These would be generated by AI based on news and portfolio
		},
	}

	// Send email notification
	if settings.EmailEnabled {
		email, err := ns.userRepo.GetUserEmail(ctx, userID)
		if err != nil {
			ns.logger.Warn("Failed to get user email", zap.Error(err))
		} else {
			if err := ns.sendDailyDigestEmail(ctx, email, digestData); err != nil {
				ns.logger.Warn("Failed to send digest email", zap.Error(err))
			}
		}
	}

	// Send push notification
	if settings.PushEnabled {
		if err := ns.sendDailyDigestPush(ctx, userID, digestData); err != nil {
			ns.logger.Warn("Failed to send digest push", zap.Error(err))
		}
	}

	ns.metrics.NotificationsSent.Add(ctx, 1, metric.WithAttributes(
		attribute.String("type", "daily_digest"),
		attribute.String("user_id", userID.String()),
	))

	return nil
}

// runWeeklySummary processes and sends weekly portfolio summaries
func (ns *NotificationScheduler) runWeeklySummary(ctx context.Context) error {
	startTime := time.Now()
	ctx, span := ns.tracer.Start(ctx, "scheduler.run_weekly_summary")
	defer span.End()

	ns.logger.Info("Starting weekly summary job")

	// Get all active users
	userIDs, err := ns.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get active users: %w", err)
	}

	// Calculate week start (last Monday)
	now := time.Now()
	weekStart := now.AddDate(0, 0, -int(now.Weekday())+1)
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, weekStart.Location())

	successCount := 0
	failureCount := 0

	// Process users in batches
	for i := 0; i < len(userIDs); i += ns.config.BatchSize {
		end := i + ns.config.BatchSize
		if end > len(userIDs) {
			end = len(userIDs)
		}
		
		batch := userIDs[i:end]
		
		for _, userID := range batch {
			if err := ns.processWeeklySummaryForUser(ctx, userID, weekStart); err != nil {
				ns.logger.Warn("Failed to process weekly summary for user",
					zap.String("user_id", userID.String()),
					zap.Error(err),
				)
				failureCount++
			} else {
				successCount++
			}
		}
	}

	duration := time.Since(startTime)
	
	// Record metrics
	ns.metrics.ScheduledJobsExecuted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("job_type", "weekly_summary"),
	))
	ns.metrics.JobExecutionDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(
		attribute.String("job_type", "weekly_summary"),
	))
	ns.metrics.UsersProcessed.Add(ctx, int64(successCount))

	ns.logger.Info("Weekly summary job completed",
		zap.Int("success_count", successCount),
		zap.Int("failure_count", failureCount),
		zap.Duration("duration", duration),
	)

	return nil
}

// processWeeklySummaryForUser generates and sends weekly summary for a user
func (ns *NotificationScheduler) processWeeklySummaryForUser(ctx context.Context, userID uuid.UUID, weekStart time.Time) error {
	ctx, span := ns.tracer.Start(ctx, "scheduler.process_weekly_summary_user", trace.WithAttributes(
		attribute.String("user_id", userID.String()),
	))
	defer span.End()

	// Check user notification settings
	settings, err := ns.userRepo.GetUserNotificationSettings(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get notification settings: %w", err)
	}

	if !settings.WeeklySummary {
		return nil
	}

	// Generate weekly summary using AI-CFO service
	summary, err := ns.aicfoService.GenerateWeeklySummary(ctx, userID, weekStart)
	if err != nil {
		return fmt.Errorf("failed to generate weekly summary: %w", err)
	}

	ns.logger.Info("Weekly summary generated",
		zap.String("user_id", userID.String()),
		zap.String("summary_id", summary.ID.String()),
	)

	// The AI-CFO service already handles notifications internally
	// This is already done in aicfo_service.go

	return nil
}

// checkPerformanceAlerts checks for significant portfolio changes and sends alerts
func (ns *NotificationScheduler) checkPerformanceAlerts(ctx context.Context) error {
	ctx, span := ns.tracer.Start(ctx, "scheduler.check_performance_alerts")
	defer span.End()

	ns.logger.Debug("Checking performance alerts")

	// Get all active users
	userIDs, err := ns.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to get active users: %w", err)
	}

	alertCount := 0

	for _, userID := range userIDs {
		settings, err := ns.userRepo.GetUserNotificationSettings(ctx, userID)
		if err != nil || !settings.PerformanceAlerts {
			continue
		}

		// Check for significant changes (would be implemented with portfolio service)
		// For now, this is a placeholder
		_ = userID
		
		// If alert triggered, send notification
		// alertCount++
	}

	ns.logger.Debug("Performance alerts check completed", zap.Int("alerts_sent", alertCount))

	return nil
}

// sendDailyDigestEmail sends the daily digest via email
func (ns *NotificationScheduler) sendDailyDigestEmail(ctx context.Context, email string, data *DailyDigestData) error {
	// Email content would be generated here
	subject := fmt.Sprintf("Daily Portfolio Digest - %s", data.Date.Format("January 2, 2006"))
	htmlContent := ns.buildDigestEmailHTML(data)
	textContent := ns.buildDigestEmailText(data)

	// Use notification service to send
	// Would need to extend NotificationService with a method for custom emails
	_ = subject
	_ = htmlContent
	_ = textContent
	_ = email

	return nil
}

// sendDailyDigestPush sends the daily digest via push notification
func (ns *NotificationScheduler) sendDailyDigestPush(ctx context.Context, userID uuid.UUID, data *DailyDigestData) error {
	title := "Your Daily Portfolio Digest"
	body := fmt.Sprintf("Portfolio update for %s. %d news items relevant to your holdings.",
		data.Date.Format("Jan 2"),
		len(data.News.PortfolioNews)+len(data.News.SectorNews),
	)

	return ns.notificationService.SendPushNotification(ctx, userID, title, body)
}

// buildDigestEmailHTML builds HTML email content for daily digest
func (ns *NotificationScheduler) buildDigestEmailHTML(data *DailyDigestData) string {
	// Simplified - would use proper email template
	return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Daily Portfolio Digest</title>
</head>
<body>
    <h1>Your Daily Portfolio Digest</h1>
    <p>%s</p>
    <h2>Market News</h2>
    <p>%s</p>
</body>
</html>`,
		data.Date.Format("January 2, 2006"),
		data.News.AISummary,
	)
}

// buildDigestEmailText builds plain text email content
func (ns *NotificationScheduler) buildDigestEmailText(data *DailyDigestData) string {
	return fmt.Sprintf("Daily Portfolio Digest - %s\n\n%s",
		data.Date.Format("January 2, 2006"),
		data.News.AISummary,
	)
}

// isQuietHours checks if current time is within user's quiet hours
func (ns *NotificationScheduler) isQuietHours(settings *NotificationSettings) bool {
	now := time.Now()
	
	start := settings.QuietHoursStart
	end := settings.QuietHoursEnd
	
	// Handle case where quiet hours span midnight
	if start.After(end) {
		return now.After(start) || now.Before(end)
	}
	
	return now.After(start) && now.Before(end)
}

// GetSchedulerStatus returns the current status of the scheduler
func (ns *NotificationScheduler) GetSchedulerStatus() map[string]interface{} {
	entries := ns.scheduler.Entries()
	
	jobs := make([]map[string]interface{}, len(entries))
	for i, entry := range entries {
		jobs[i] = map[string]interface{}{
			"next_run": entry.Next,
			"prev_run": entry.Prev,
		}
	}
	
	return map[string]interface{}{
		"running":    ns.running,
		"job_count":  len(entries),
		"jobs":       jobs,
	}
}

// initSchedulerMetrics initializes metrics
func initSchedulerMetrics(meter metric.Meter) (*SchedulerMetrics, error) {
	scheduledJobsExecuted, err := meter.Int64Counter("notification_scheduler_jobs_executed_total",
		metric.WithDescription("Total number of scheduled jobs executed"))
	if err != nil {
		return nil, err
	}

	jobExecutionDuration, err := meter.Float64Histogram("notification_scheduler_job_duration_seconds",
		metric.WithDescription("Duration of scheduled job execution"))
	if err != nil {
		return nil, err
	}

	jobFailures, err := meter.Int64Counter("notification_scheduler_job_failures_total",
		metric.WithDescription("Total number of job failures"))
	if err != nil {
		return nil, err
	}

	usersProcessed, err := meter.Int64Counter("notification_scheduler_users_processed_total",
		metric.WithDescription("Total number of users processed"))
	if err != nil {
		return nil, err
	}

	notificationsSent, err := meter.Int64Counter("notification_scheduler_notifications_sent_total",
		metric.WithDescription("Total number of notifications sent by scheduler"))
	if err != nil {
		return nil, err
	}

	return &SchedulerMetrics{
		ScheduledJobsExecuted: scheduledJobsExecuted,
		JobExecutionDuration:  jobExecutionDuration,
		JobFailures:           jobFailures,
		UsersProcessed:        usersProcessed,
		NotificationsSent:     notificationsSent,
	}, nil
}

// DefaultSchedulerConfig returns default scheduler configuration
func DefaultSchedulerConfig() *SchedulerConfig {
	return &SchedulerConfig{
		DailyNewsSummaryTime:     "0 8 * * *",  // 8 AM daily
		WeeklySummaryTime:        "0 7 * * 1",  // 7 AM Monday
		MarketOpenNotifications:  true,
		EnableDailyDigest:        true,
		EnableWeeklySummary:      true,
		EnablePerformanceAlerts:  true,
		BatchSize:                50,
		ConcurrencyLimit:         10,
		RetryAttempts:            3,
		RetryDelay:               5 * time.Second,
	}
}

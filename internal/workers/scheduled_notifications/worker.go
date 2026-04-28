package scheduled_notifications

import (
	"context"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/infrastructure/repositories"
	"go.uber.org/zap"
)

// UserRepo fetches users for notification targeting.
type UserRepo interface {
	GetUnverifiedUsersForNotification(ctx context.Context) ([]repositories.NotificationUser, error)
	GetAllActiveUsers(ctx context.Context) ([]repositories.NotificationUser, error)
	GetUsersWithNoDeposits(ctx context.Context) ([]repositories.NotificationUser, error)
}

// PushSender sends a push notification to a single user.
type PushSender interface {
	SendToUser(ctx context.Context, userID uuid.UUID, title, body string, data map[string]interface{}) error
}

// Worker runs scheduled push notification jobs.
type Worker struct {
	userRepo         UserRepo
	pushSender       PushSender
	logger           *zap.Logger
	lastKYCDate      string // "YYYY-MM-DD" of last KYC send
	lastEngageDate   string // "YYYY-MM-DD" of last engagement send
	lastDepositDate  string // "YYYY-MM-DD" of last deposit reminder send
}

func NewWorker(userRepo UserRepo, pushSender PushSender, logger *zap.Logger) *Worker {
	return &Worker{userRepo: userRepo, pushSender: pushSender, logger: logger}
}

// Start blocks and runs the scheduler loop until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.logger.Info("Scheduled notifications worker started")

	// Run immediately on startup so we don't wait up to 1 hour for first tick.
	w.runIfDue(ctx)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Scheduled notifications worker stopped")
			return
		case <-ticker.C:
			w.runIfDue(ctx)
		}
	}
}

// runIfDue checks the current time and fires the appropriate jobs.
func (w *Worker) runIfDue(ctx context.Context) {
	now := time.Now().UTC()
	hour := now.Hour()
	weekday := now.Weekday()
	today := now.Format("2006-01-02")

	// KYC reminder: Mon / Wed / Fri at 10:00 UTC — once per day
	if hour == 10 &&
		(weekday == time.Monday || weekday == time.Wednesday || weekday == time.Friday) &&
		w.lastKYCDate != today {
		w.lastKYCDate = today
		go w.sendKYCReminders(ctx)
	}

	// Deposit reminder: Tue / Thu / Sat at 14:00 UTC — once per day
	if hour == 14 &&
		(weekday == time.Tuesday || weekday == time.Thursday || weekday == time.Saturday) &&
		w.lastDepositDate != today {
		w.lastDepositDate = today
		go w.sendDepositReminders(ctx)
	}

	// Daily engagement: every day at 18:00 UTC — once per day
	if hour == 18 && w.lastEngageDate != today {
		w.lastEngageDate = today
		go w.sendDailyEngagement(ctx)
	}
}

// kycMessages are rotated so users don't see the same copy every time.
var kycMessages = []struct{ title, body string }{
	{
		title: "Unlock your full Rail experience",
		body:  "Verify your identity in under 5 minutes to withdraw to your bank, get a Rail Card, and more.",
	},
	{
		title: "One step away from full access",
		body:  "Complete your identity verification to unlock bank withdrawals, deposits, and your Rail Debit Card.",
	},
	{
		title: "Your Rail Card is waiting",
		body:  "Finish KYC verification to activate your Rail Debit Card and start spending your crypto anywhere.",
	},
}

// depositMessages nudge verified users who haven't made their first deposit.
var depositMessages = []struct{ title, body string }{
	{
		title: "Your money could be working right now",
		body:  "Make your first deposit and Rail automatically splits it — 70% to spend, 30% to invest.",
	},
	{
		title: "Start with as little as $1",
		body:  "Deposit any amount and watch Rail put your money to work instantly. No setup needed.",
	},
	{
		title: "You're all set up — just add funds",
		body:  "Your Rail account is ready. Deposit via bank transfer or crypto to activate your 70/30 split.",
	},
	{
		title: "Don't let your money sit idle",
		body:  "Every deposit on Rail earns yield automatically. Fund your account to get started.",
	},
}

// engagementMessages are rotated daily to keep things fresh.
var engagementMessages = []struct{ title, body string }{
	{
		title: "Your portfolio is moving",
		body:  "Check in on your stash performance and see what the market is doing today.",
	},
	{
		title: "Good evening",
		body:  "See how your investments performed today and explore what's trending on Rail.",
	},
	{
		title: "Markets are open",
		body:  "Stay on top of your finances — open Rail to review your balance and recent activity.",
	},
	{
		title: "Rail tip of the day",
		body:  "Did you know you can send money to anyone with a RailTag instantly? Try it today.",
	},
	{
		title: "Your money, your rules",
		body:  "Check your spending stash and investment positions — all in one place on Rail.",
	},
}

func (w *Worker) sendDepositReminders(ctx context.Context) {
	// TODO(R3-L4): Check user notification preferences before sending.
	// Each user should be able to opt out of deposit reminders, KYC reminders,
	// and engagement notifications independently. Query a notification_preferences
	// table (or user settings) and skip users who have opted out of this category.
	users, err := w.userRepo.GetUsersWithNoDeposits(ctx)
	if err != nil {
		w.logger.Error("Failed to fetch users with no deposits", zap.Error(err))
		return
	}
	if len(users) == 0 {
		return
	}

	msg := depositMessages[time.Now().UTC().YearDay()%len(depositMessages)]
	data := map[string]interface{}{"type": "deposit_reminder", "screen": "/(tabs)/funding"}

	w.logger.Info("Sending deposit reminder notifications", zap.Int("users", len(users)))

	for _, u := range users {
		if err := w.pushSender.SendToUser(ctx, u.ID, msg.title, msg.body, data); err != nil {
			w.logger.Warn("Failed to send deposit reminder", zap.String("user_id", u.ID.String()), zap.Error(err))
		}
	}
}

func (w *Worker) sendKYCReminders(ctx context.Context) {
	users, err := w.userRepo.GetUnverifiedUsersForNotification(ctx)
	if err != nil {
		w.logger.Error("Failed to fetch unverified users", zap.Error(err))
		return
	}
	if len(users) == 0 {
		return
	}

	msg := kycMessages[time.Now().UTC().Day()%len(kycMessages)]
	data := map[string]interface{}{"type": "kyc", "screen": "/kyc"}

	w.logger.Info("Sending KYC reminder notifications", zap.Int("users", len(users)))

	for _, u := range users {
		if err := w.pushSender.SendToUser(ctx, u.ID, msg.title, msg.body, data); err != nil {
			w.logger.Warn("Failed to send KYC reminder", zap.String("user_id", u.ID.String()), zap.Error(err))
		}
	}
}

func (w *Worker) sendDailyEngagement(ctx context.Context) {
	users, err := w.userRepo.GetAllActiveUsers(ctx)
	if err != nil {
		w.logger.Error("Failed to fetch active users", zap.Error(err))
		return
	}
	if len(users) == 0 {
		return
	}

	msg := engagementMessages[time.Now().UTC().YearDay()%len(engagementMessages)]
	data := map[string]interface{}{"type": "engagement", "screen": "/(tabs)"}

	w.logger.Info("Sending daily engagement notifications", zap.Int("users", len(users)))

	// Shuffle to avoid thundering-herd on the push API for large user bases.
	shuffled := make([]repositories.NotificationUser, len(users))
	copy(shuffled, users)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	for _, u := range shuffled {
		if err := w.pushSender.SendToUser(ctx, u.ID, msg.title, msg.body, data); err != nil {
			w.logger.Warn("Failed to send engagement notification", zap.String("user_id", u.ID.String()), zap.Error(err))
		}
	}
}

package miriam

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NotificationMode controls how Miriam delivers notifications.
type NotificationMode string

const (
	ModeImmediate NotificationMode = "immediate" // send each action immediately
	ModeBatched   NotificationMode = "batched"   // collect within 4-hour windows
	ModeDigest    NotificationMode = "digest"    // daily summary at preferred time
	ModeSilent    NotificationMode = "silent"    // no notifications, check in-app
)

// NotificationPreferences stores a user's notification preferences.
type NotificationPreferences struct {
	UserID           uuid.UUID        `json:"user_id"`
	Mode             NotificationMode `json:"mode"`
	QuietHoursStart  int              `json:"quiet_hours_start"`  // 0-23
	QuietHoursEnd    int              `json:"quiet_hours_end"`    // 0-23
	DigestHour       int              `json:"digest_hour"`        // preferred hour for digest
	TriggerTypePrefs map[string]bool  `json:"trigger_type_prefs"` // per-trigger opt-out
}

// NotificationDigest is a batch of notifications for digest mode.
type NotificationDigest struct {
	UserID      uuid.UUID    `json:"user_id"`
	GeneratedAt time.Time    `json:"generated_at"`
	PeriodStart time.Time    `json:"period_start"`
	PeriodEnd   time.Time    `json:"period_end"`
	Items       []DigestItem `json:"items"`
	Summary     string       `json:"summary"`
}

// DigestItem is a single item in a notification digest.
type DigestItem struct {
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// NotificationDigestStore persists digest batches.
type NotificationDigestStore interface {
	SaveDigest(ctx context.Context, d *NotificationDigest) error
	GetRecentDigests(ctx context.Context, userID uuid.UUID, limit int) ([]NotificationDigest, error)
}

// NotificationPrefStore persists user notification preferences.
type NotificationPrefStore interface {
	GetPreferences(ctx context.Context, userID uuid.UUID) (*NotificationPreferences, error)
	SavePreferences(ctx context.Context, p *NotificationPreferences) error
}

// NotificationDispatcher handles delivery with mode-aware logic.
type NotificationDispatcher struct {
	prefStore   NotificationPrefStore
	digestStore NotificationDigestStore
	notifier    Notifier
	mu          sync.Mutex
	batch       map[uuid.UUID][]DigestItem
	logger      *zap.Logger
}

// NewNotificationDispatcher creates a dispatcher.
func NewNotificationDispatcher(
	prefStore NotificationPrefStore,
	digestStore NotificationDigestStore,
	notifier Notifier,
	logger *zap.Logger,
) *NotificationDispatcher {
	return &NotificationDispatcher{
		prefStore: prefStore, digestStore: digestStore,
		notifier: notifier, batch: make(map[uuid.UUID][]DigestItem), logger: logger,
	}
}

// Notify delivers a notification respecting user preferences.
func (d *NotificationDispatcher) Notify(ctx context.Context, userID uuid.UUID, title, message, triggerType string) error {
	prefs, err := d.prefStore.GetPreferences(ctx, userID)
	if err != nil || prefs == nil {
		// Default to immediate if no preferences
		return d.notifier.SendGenericNotification(ctx, userID, title, message)
	}

	// Check quiet hours
	if d.isInQuietHours(prefs, time.Now().UTC()) {
		if prefs.Mode == ModeSilent {
			return nil // silently drop
		}
		// In quiet hours, queue for digest
		d.queueForDigest(userID, title, message, triggerType)
		return nil
	}

	// Check per-trigger opt-out
	if prefs.TriggerTypePrefs != nil && !prefs.TriggerTypePrefs[triggerType] {
		return nil // user opted out of this trigger type
	}

	switch prefs.Mode {
	case ModeImmediate:
		return d.notifier.SendGenericNotification(ctx, userID, title, message)
	case ModeBatched:
		d.queueForBatch(userID, title, message, triggerType)
		return nil
	case ModeDigest:
		d.queueForDigest(userID, title, message, triggerType)
		return nil
	case ModeSilent:
		return nil
	default:
		return d.notifier.SendGenericNotification(ctx, userID, title, message)
	}
}

// FlushBatches sends all batched notifications for users.
func (d *NotificationDispatcher) FlushBatches(ctx context.Context) int {
	d.mu.Lock()
	items := d.batch
	d.batch = make(map[uuid.UUID][]DigestItem)
	d.mu.Unlock()

	sent := 0
	for userID, userItems := range items {
		if len(userItems) == 0 {
			continue
		}
		title := "Miriam summary"
		message := buildBatchedMessage(userItems)
		if d.notifier != nil {
			if err := d.notifier.SendGenericNotification(ctx, userID, title, message); err != nil && d.logger != nil {
				d.logger.Warn("batch notification failed", zap.Error(err))
			} else {
				sent++
			}
		}
	}
	return sent
}

// FlushDigests generates and sends daily digest notifications for users whose preferred hour matches.
func (d *NotificationDispatcher) FlushDigests(ctx context.Context, hour int) int {
	d.mu.Lock()
	items := d.batch
	d.batch = make(map[uuid.UUID][]DigestItem)
	d.mu.Unlock()

	sent := 0
	for userID, userItems := range items {
		if len(userItems) == 0 {
			continue
		}
		// Only flush for users whose preferred digest hour matches
		if prefs, err := d.prefStore.GetPreferences(ctx, userID); err == nil && prefs != nil {
			if prefs.Mode == ModeDigest && prefs.DigestHour != hour {
				// Not this user's digest hour — put items back
				d.mu.Lock()
				d.batch[userID] = append(d.batch[userID], userItems...)
				d.mu.Unlock()
				continue
			}
		}
		digest := &NotificationDigest{
			UserID:      userID,
			GeneratedAt: time.Now().UTC(),
			PeriodStart: time.Now().UTC().Add(-24 * time.Hour),
			PeriodEnd:   time.Now().UTC(),
			Items:       userItems,
			Summary:     buildDigestSummary(userItems),
		}
		if d.digestStore != nil {
			_ = d.digestStore.SaveDigest(ctx, digest)
		}
		if d.notifier != nil {
			message := buildDigestMessage(digest)
			if err := d.notifier.SendGenericNotification(ctx, userID, "Miriam daily summary", message); err != nil && d.logger != nil {
				d.logger.Warn("digest notification failed", zap.Error(err))
			} else {
				sent++
			}
		}
	}
	return sent
}

func (d *NotificationDispatcher) isInQuietHours(prefs *NotificationPreferences, now time.Time) bool {
	currentHour := now.Hour()
	start := prefs.QuietHoursStart
	end := prefs.QuietHoursEnd

	if start == end {
		return false // no quiet hours set
	}

	if start < end {
		return currentHour >= start && currentHour < end
	}
	// Quiet hours span midnight (e.g., 22:00 to 07:00)
	return currentHour >= start || currentHour < end
}

func (d *NotificationDispatcher) queueForBatch(userID uuid.UUID, title, message, triggerType string) {
	d.mu.Lock()
	d.batch[userID] = append(d.batch[userID], DigestItem{
		Type:      triggerType,
		Title:     title,
		Message:   message,
		Timestamp: time.Now().UTC(),
	})
	d.mu.Unlock()
}

func (d *NotificationDispatcher) queueForDigest(userID uuid.UUID, title, message, triggerType string) {
	d.mu.Lock()
	d.batch[userID] = append(d.batch[userID], DigestItem{
		Type:      triggerType,
		Title:     title,
		Message:   message,
		Timestamp: time.Now().UTC(),
	})
	d.mu.Unlock()
}

func buildBatchedMessage(items []DigestItem) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d update", len(items)))
	if len(items) > 1 {
		b.WriteRune('s')
	}
	b.WriteString(": ")
	for i, item := range items {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(item.Message)
	}
	return b.String()
}

func buildDigestSummary(items []DigestItem) string {
	if len(items) == 0 {
		return "No updates today."
	}
	return fmt.Sprintf("%d money update", len(items))
}

func buildDigestMessage(digest *NotificationDigest) string {
	var b strings.Builder
	b.WriteString(digest.Summary)
	b.WriteString(": ")
	for i, item := range digest.Items {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(item.Message)
	}
	return b.String()
}

// Serialize marshals notification preferences to JSON.
func (p *NotificationPreferences) Serialize() json.RawMessage {
	b, err := json.Marshal(p)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// DeserializePrefs unmarshals notification preferences from JSON.
func DeserializePrefs(raw json.RawMessage) (*NotificationPreferences, error) {
	var p NotificationPreferences
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

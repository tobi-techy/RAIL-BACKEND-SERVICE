package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
)

type GrowthEngineRepository struct {
	db *sql.DB
}

func NewGrowthEngineRepository(db *sql.DB) *GrowthEngineRepository {
	return &GrowthEngineRepository{db: db}
}

func (r *GrowthEngineRepository) TrackEvent(ctx context.Context, event *entities.UserEvent) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("marshal growth event metadata: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO user_events (id, user_id, event_name, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		event.ID, event.UserID, string(event.EventName), metadata, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("track growth event: %w", err)
	}
	return nil
}

func (r *GrowthEngineRepository) ApplyEventToLifecycle(ctx context.Context, event *entities.UserEvent) error {
	stage := lifecycleStageForEvent(event.EventName)
	query := `
		INSERT INTO user_lifecycle (
			user_id, lifecycle_stage, last_active_at, kyc_started_at, kyc_completed_at,
			first_deposit_started_at, first_deposit_at, last_deposit_at, miriam_last_used_at,
			allocation_enabled, created_at, updated_at
		)
		VALUES (
			$1, CASE WHEN $2 = '' THEN 'signed_up' ELSE $2 END,
			CASE WHEN $3 THEN $4::timestamptz ELSE NULL END,
			CASE WHEN $5 THEN $4::timestamptz ELSE NULL END,
			CASE WHEN $6 THEN $4::timestamptz ELSE NULL END,
			CASE WHEN $7 THEN $4::timestamptz ELSE NULL END,
			CASE WHEN $8 THEN $4::timestamptz ELSE NULL END,
			CASE WHEN $8 THEN $4::timestamptz ELSE NULL END,
			CASE WHEN $9 THEN $4::timestamptz ELSE NULL END,
			$10, $4, $4
		)
		ON CONFLICT (user_id) DO UPDATE SET
			lifecycle_stage = CASE WHEN EXCLUDED.lifecycle_stage = '' THEN user_lifecycle.lifecycle_stage ELSE EXCLUDED.lifecycle_stage END,
			last_active_at = CASE WHEN $3 THEN GREATEST(COALESCE(user_lifecycle.last_active_at, $4), $4) ELSE user_lifecycle.last_active_at END,
			kyc_started_at = CASE WHEN $5 THEN COALESCE(user_lifecycle.kyc_started_at, $4) ELSE user_lifecycle.kyc_started_at END,
			kyc_completed_at = CASE WHEN $6 THEN COALESCE(user_lifecycle.kyc_completed_at, $4) ELSE user_lifecycle.kyc_completed_at END,
			first_deposit_started_at = CASE WHEN $7 THEN COALESCE(user_lifecycle.first_deposit_started_at, $4) ELSE user_lifecycle.first_deposit_started_at END,
			first_deposit_at = CASE WHEN $8 THEN COALESCE(user_lifecycle.first_deposit_at, $4) ELSE user_lifecycle.first_deposit_at END,
			last_deposit_at = CASE WHEN $8 THEN GREATEST(COALESCE(user_lifecycle.last_deposit_at, $4), $4) ELSE user_lifecycle.last_deposit_at END,
			miriam_last_used_at = CASE WHEN $9 THEN GREATEST(COALESCE(user_lifecycle.miriam_last_used_at, $4), $4) ELSE user_lifecycle.miriam_last_used_at END,
			allocation_enabled = user_lifecycle.allocation_enabled OR $10,
			updated_at = $4`
	_, err := r.db.ExecContext(
		ctx,
		query,
		event.UserID,
		string(stage),
		isActiveEvent(event.EventName),
		event.CreatedAt,
		event.EventName == entities.GrowthEventKYCStarted,
		event.EventName == entities.GrowthEventKYCCompleted,
		event.EventName == entities.GrowthEventDepositStarted,
		event.EventName == entities.GrowthEventDepositCompleted,
		event.EventName == entities.GrowthEventMiriamUsed,
		event.EventName == entities.GrowthEventAllocationEnabled,
	)
	if err != nil {
		return fmt.Errorf("apply growth event to lifecycle: %w", err)
	}
	return nil
}

func (r *GrowthEngineRepository) MarkConversions(ctx context.Context, userID uuid.UUID, eventName entities.GrowthEventName) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		WITH updated AS (
			UPDATE campaign_deliveries cd
			SET status = $1, converted_at = $2
			FROM campaigns c
			WHERE c.id = cd.campaign_id
			  AND cd.user_id = $3
			  AND c.conversion_event = $4
			  AND cd.converted_at IS NULL
			  AND cd.status IN ($5, $6)
			RETURNING cd.id, cd.user_id, cd.campaign_id
		)
		INSERT INTO campaign_conversions (id, delivery_id, user_id, campaign_id, conversion_event, created_at)
		SELECT gen_random_uuid(), id, user_id, campaign_id, $4, $2
		FROM updated
		ON CONFLICT (delivery_id) DO NOTHING`,
		entities.GrowthDeliveryConverted,
		now,
		userID,
		string(eventName),
		entities.GrowthDeliverySent,
		entities.GrowthDeliveryQueued,
	)
	if err != nil {
		return fmt.Errorf("mark growth conversions: %w", err)
	}
	return nil
}

func (r *GrowthEngineRepository) ListGrowthCandidates(ctx context.Context, limit int) ([]entities.GrowthUserSnapshot, error) {
	if limit <= 0 {
		limit = 1000
	}
	query := `
		SELECT
			u.id,
			u.email,
			u.phone,
			COALESCE(u.first_name, '') AS first_name,
			u.created_at,
			u.last_login_at,
			COALESCE(u.kyc_status, '') AS kyc_status,
			COALESCE(ul.kyc_started_at, u.kyc_submitted_at),
			COALESCE(ul.kyc_completed_at, u.kyc_approved_at),
			ul.first_deposit_started_at,
			COALESCE(ul.first_deposit_at, fd.first_deposit_at),
			COALESCE(ul.last_deposit_at, fd.last_deposit_at),
			ul.miriam_last_used_at,
			COALESCE(ul.last_active_at, u.last_login_at),
			COALESCE(ul.allocation_enabled, false),
			COALESCE(ul.current_segment, '')
		FROM users u
		LEFT JOIN user_lifecycle ul ON ul.user_id = u.id
		LEFT JOIN LATERAL (
			SELECT MIN(created_at) AS first_deposit_at, MAX(created_at) AS last_deposit_at
			FROM deposits d
			WHERE d.user_id = u.id
			  AND d.status IN ('confirmed', 'completed', 'broker_funded')
		) fd ON true
		WHERE u.is_active = true
		  AND u.anonymized_at IS NULL
		  AND u.email <> ''
		ORDER BY COALESCE(ul.updated_at, u.updated_at, u.created_at) ASC
		LIMIT $1`
	rows, err := r.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list growth candidates: %w", err)
	}
	defer rows.Close()

	out := make([]entities.GrowthUserSnapshot, 0)
	for rows.Next() {
		var s entities.GrowthUserSnapshot
		var phone sql.NullString
		var lastLogin, kycStarted, kycCompleted, firstDepositStarted, firstDeposit, lastDeposit, miriamUsed, lastActive sql.NullTime
		var segment string
		if err := rows.Scan(
			&s.UserID,
			&s.Email,
			&phone,
			&s.FirstName,
			&s.CreatedAt,
			&lastLogin,
			&s.KYCStatus,
			&kycStarted,
			&kycCompleted,
			&firstDepositStarted,
			&firstDeposit,
			&lastDeposit,
			&miriamUsed,
			&lastActive,
			&s.AllocationEnabled,
			&segment,
		); err != nil {
			return nil, fmt.Errorf("scan growth candidate: %w", err)
		}
		if phone.Valid {
			v := phone.String
			s.Phone = &v
		}
		s.LastLoginAt = nullableTime(lastLogin)
		s.KYCStartedAt = nullableTime(kycStarted)
		s.KYCCompletedAt = nullableTime(kycCompleted)
		s.FirstDepositStartedAt = nullableTime(firstDepositStarted)
		s.FirstDepositAt = nullableTime(firstDeposit)
		s.LastDepositAt = nullableTime(lastDeposit)
		s.MiriamLastUsedAt = nullableTime(miriamUsed)
		s.LastActiveAt = nullableTime(lastActive)
		s.CurrentSegment = entities.GrowthSegment(segment)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate growth candidates: %w", err)
	}
	return out, nil
}

func (r *GrowthEngineRepository) UpsertSegment(ctx context.Context, userID uuid.UUID, stage entities.UserLifecycleStage, segment entities.GrowthSegment, score int, assignedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_lifecycle (user_id, lifecycle_stage, current_segment, reactivation_score, segment_assigned_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			lifecycle_stage = $2,
			current_segment = $3,
			reactivation_score = $4,
			segment_assigned_at = CASE WHEN user_lifecycle.current_segment IS DISTINCT FROM $3 THEN $5 ELSE user_lifecycle.segment_assigned_at END,
			updated_at = $5`,
		userID, string(stage), string(segment), score, assignedAt)
	if err != nil {
		return fmt.Errorf("upsert growth segment: %w", err)
	}
	return nil
}

func (r *GrowthEngineRepository) ListActiveCampaignsBySegment(ctx context.Context, segment entities.GrowthSegment) ([]entities.GrowthCampaign, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, segment, channel, template_key, subject, body, cta, cooldown_days,
		       conversion_event, from_email, from_name, reply_to, is_active, created_at, updated_at
		FROM campaigns
		WHERE segment = $1 AND is_active = true
		ORDER BY priority ASC, created_at ASC`,
		string(segment))
	if err != nil {
		return nil, fmt.Errorf("list active growth campaigns: %w", err)
	}
	defer rows.Close()

	campaigns := make([]entities.GrowthCampaign, 0)
	for rows.Next() {
		var c entities.GrowthCampaign
		if err := rows.Scan(&c.ID, &c.Name, &c.Segment, &c.Channel, &c.TemplateKey, &c.Subject, &c.Body, &c.CTA, &c.CooldownDays, &c.ConversionEvent, &c.FromEmail, &c.FromName, &c.ReplyTo, &c.IsActive, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan growth campaign: %w", err)
		}
		campaigns = append(campaigns, c)
	}
	return campaigns, rows.Err()
}

func (r *GrowthEngineRepository) HasRecentDelivery(ctx context.Context, userID, campaignID uuid.UUID, cooldown time.Duration) (bool, error) {
	if cooldown <= 0 {
		cooldown = 24 * time.Hour
	}
	var exists bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM campaign_deliveries
			WHERE user_id = $1
			  AND campaign_id = $2
			  AND created_at >= $3
			  AND status IN ('queued', 'sent', 'converted')
		)`,
		userID, campaignID, time.Now().UTC().Add(-cooldown)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check recent growth delivery: %w", err)
	}
	return exists, nil
}

func (r *GrowthEngineRepository) CreateDelivery(ctx context.Context, delivery *entities.CampaignDelivery) error {
	if delivery.ID == uuid.Nil {
		delivery.ID = uuid.New()
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO campaign_deliveries (
			id, user_id, campaign_id, segment, channel, status, error, rendered_to, subject, body, sent_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''), $11, $12)`,
		delivery.ID, delivery.UserID, delivery.CampaignID, string(delivery.Segment), string(delivery.Channel),
		string(delivery.Status), delivery.Error, delivery.RenderedTo, delivery.Subject, delivery.Body, delivery.SentAt, delivery.CreatedAt)
	if err != nil {
		return fmt.Errorf("create growth delivery: %w", err)
	}
	return nil
}

func (r *GrowthEngineRepository) UpdateDeliveryStatus(ctx context.Context, deliveryID uuid.UUID, status entities.GrowthDeliveryStatus, errMessage string, sentAt *time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE campaign_deliveries
		SET status = $2, error = NULLIF($3, ''), sent_at = COALESCE($4, sent_at)
		WHERE id = $1`,
		deliveryID, string(status), errMessage, sentAt)
	if err != nil {
		return fmt.Errorf("update growth delivery status: %w", err)
	}
	return nil
}

func (r *GrowthEngineRepository) ListManualWhatsAppLeads(ctx context.Context, limit int) ([]entities.WhatsAppGrowthLead, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			COALESCE(NULLIF(u.first_name, ''), u.email) AS name,
			COALESCE(u.phone, '') AS phone,
			cd.segment,
			COALESCE(cd.body, '') AS suggested_message,
			COALESCE(ul.last_active_at, u.last_login_at, u.created_at) AS last_action_at,
			cd.created_at
		FROM campaign_deliveries cd
		JOIN users u ON u.id = cd.user_id
		LEFT JOIN user_lifecycle ul ON ul.user_id = u.id
		WHERE cd.channel = 'manual_whatsapp'
		  AND cd.status = 'queued'
		  AND u.phone IS NOT NULL
		  AND u.phone <> ''
		ORDER BY cd.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list manual whatsapp leads: %w", err)
	}
	defer rows.Close()

	leads := make([]entities.WhatsAppGrowthLead, 0)
	for rows.Next() {
		var lead entities.WhatsAppGrowthLead
		var lastAction sql.NullTime
		if err := rows.Scan(&lead.Name, &lead.Phone, &lead.Segment, &lead.SuggestedMessage, &lastAction, &lead.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan manual whatsapp lead: %w", err)
		}
		lead.LastActionAt = nullableTime(lastAction)
		leads = append(leads, lead)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate manual whatsapp leads: %w", err)
	}
	return leads, nil
}

func lifecycleStageForEvent(eventName entities.GrowthEventName) entities.UserLifecycleStage {
	switch eventName {
	case entities.GrowthEventAppOpened:
		return entities.StageOpenedApp
	case entities.GrowthEventKYCStarted:
		return entities.StageKYCStarted
	case entities.GrowthEventKYCCompleted:
		return entities.StageKYCCompleted
	case entities.GrowthEventDepositCompleted:
		return entities.StageFirstDepositDone
	case entities.GrowthEventInactive7DaysDetected:
		return entities.StageDormant
	case entities.GrowthEventInactive14DaysDetected:
		return entities.StageChurned
	default:
		return ""
	}
}

func isActiveEvent(eventName entities.GrowthEventName) bool {
	switch eventName {
	case entities.GrowthEventAppOpened, entities.GrowthEventKYCStarted, entities.GrowthEventKYCCompleted,
		entities.GrowthEventDepositStarted, entities.GrowthEventDepositCompleted, entities.GrowthEventMiriamUsed,
		entities.GrowthEventAllocationEnabled, entities.GrowthEventReactivated:
		return true
	default:
		return false
	}
}

func nullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	v := value.Time
	return &v
}

package billpay

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Beneficiary is a saved bill-payment target.
type Beneficiary struct {
	ID            uuid.UUID  `json:"id"`
	Label         string     `json:"label"`
	Category      string     `json:"category"`
	Recipient     string     `json:"recipient"`
	NetworkID     string     `json:"network_id,omitempty"`
	ProdID        string     `json:"prod_id,omitempty"`
	ElectID       string     `json:"elect_id,omitempty"`
	RecipientName string     `json:"recipient_name,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
}

// SaveBeneficiary upserts a saved bill target for a user.
func (s *Service) SaveBeneficiary(ctx context.Context, userID uuid.UUID, b Beneficiary) (*Beneficiary, error) {
	category := strings.ToLower(strings.TrimSpace(b.Category))
	if !IsCategorySupported(category) {
		return nil, fmt.Errorf("unsupported bill category: %q", b.Category)
	}
	if strings.TrimSpace(b.Label) == "" || strings.TrimSpace(b.Recipient) == "" {
		return nil, fmt.Errorf("label and recipient are required")
	}
	id := uuid.New()
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO bill_beneficiaries (id, user_id, label, product_category, recipient, network_id, prod_id, elect_id, recipient_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (user_id, product_category, recipient)
		DO UPDATE SET label=EXCLUDED.label, network_id=EXCLUDED.network_id, prod_id=EXCLUDED.prod_id,
			elect_id=EXCLUDED.elect_id, recipient_name=EXCLUDED.recipient_name, updated_at=NOW()
		RETURNING id`,
		id, userID, b.Label, category, b.Recipient, nullStr(b.NetworkID), nullStr(b.ProdID), nullStr(b.ElectID), nullStr(b.RecipientName)).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("failed to save beneficiary: %w", err)
	}
	b.ID = id
	b.Category = category
	return &b, nil
}

// ListBeneficiaries returns a user's saved bill targets, optionally filtered.
func (s *Service) ListBeneficiaries(ctx context.Context, userID uuid.UUID, category string) ([]Beneficiary, error) {
	query := `SELECT id, label, product_category, recipient, COALESCE(network_id,''), COALESCE(prod_id,''), COALESCE(elect_id,''), COALESCE(recipient_name,''), last_used_at
		FROM bill_beneficiaries WHERE user_id=$1`
	args := []interface{}{userID}
	if c := strings.ToLower(strings.TrimSpace(category)); c != "" {
		query += ` AND product_category=$2`
		args = append(args, c)
	}
	query += ` ORDER BY last_used_at DESC NULLS LAST, label ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Beneficiary
	for rows.Next() {
		var b Beneficiary
		var lastUsed sql.NullTime
		if err := rows.Scan(&b.ID, &b.Label, &b.Category, &b.Recipient, &b.NetworkID, &b.ProdID, &b.ElectID, &b.RecipientName, &lastUsed); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			b.LastUsedAt = &lastUsed.Time
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Mandate is a per-category authorization letting Miriam pay under a cap
// without a fresh Face ID step-up.
type Mandate struct {
	ID               uuid.UUID `json:"id"`
	Category         string    `json:"category"`
	PerPaymentCapNGN float64   `json:"per_payment_cap_ngn"`
	DailyCapNGN      float64   `json:"daily_cap_ngn,omitempty"`
	AllowAuto        bool      `json:"allow_auto"`
	Status           string    `json:"status"`
	ConsentStampedAt time.Time `json:"consent_stamped_at"`
}

// SetMandate creates or updates a bill-pay mandate. Callers MUST have verified
// an in-app Face ID / passcode session before invoking; consent_stamped_at is
// set to now to anchor the reauthorization window.
func (s *Service) SetMandate(ctx context.Context, userID uuid.UUID, m Mandate) (*Mandate, error) {
	category := strings.ToLower(strings.TrimSpace(m.Category))
	if category != MandateAll && !IsCategorySupported(category) {
		return nil, fmt.Errorf("unsupported mandate category: %q", m.Category)
	}
	if m.PerPaymentCapNGN <= 0 {
		return nil, fmt.Errorf("per-payment cap must be positive")
	}
	if s.cfg.MaxAmountNGN > 0 && m.PerPaymentCapNGN > s.cfg.MaxAmountNGN {
		return nil, fmt.Errorf("per-payment cap cannot exceed ₦%.0f", s.cfg.MaxAmountNGN)
	}
	id := uuid.New()
	var daily interface{}
	if m.DailyCapNGN > 0 {
		daily = m.DailyCapNGN
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO bill_pay_mandates (id, user_id, product_category, per_payment_cap_ngn, daily_cap_ngn, allow_auto, status, consent_stamped_at)
		VALUES ($1,$2,$3,$4,$5,$6,'active',NOW())
		ON CONFLICT (user_id, product_category)
		DO UPDATE SET per_payment_cap_ngn=EXCLUDED.per_payment_cap_ngn, daily_cap_ngn=EXCLUDED.daily_cap_ngn,
			allow_auto=EXCLUDED.allow_auto, status='active', consent_stamped_at=NOW(), updated_at=NOW()
		RETURNING id`,
		id, userID, category, m.PerPaymentCapNGN, daily, m.AllowAuto).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("failed to set mandate: %w", err)
	}
	m.ID = id
	m.Category = category
	m.Status = "active"
	m.ConsentStampedAt = time.Now().UTC()
	return &m, nil
}

// GetMandate returns the active mandate for a category, falling back to the
// blanket 'all' mandate if no category-specific one exists.
func (s *Service) GetMandate(ctx context.Context, userID uuid.UUID, category string) (*Mandate, error) {
	category = strings.ToLower(strings.TrimSpace(category))
	if category != MandateAll && !IsCategorySupported(category) {
		return nil, fmt.Errorf("unsupported mandate category: %q", category)
	}
	var m Mandate
	var daily sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, product_category, per_payment_cap_ngn, daily_cap_ngn, allow_auto, status, consent_stamped_at
		FROM bill_pay_mandates
		WHERE user_id=$1 AND status='active' AND product_category IN ($2, $3)
		ORDER BY (product_category = $2) DESC LIMIT 1`,
		userID, category, MandateAll).Scan(
		&m.ID, &m.Category, &m.PerPaymentCapNGN, &daily, &m.AllowAuto, &m.Status, &m.ConsentStampedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if daily.Valid {
		m.DailyCapNGN = daily.Float64
	}
	return &m, nil
}

// MandateReauthorizationWindow bounds how long a mandate's consent stays valid
// without a fresh in-app Face ID step-up.
const MandateReauthorizationWindow = 90 * 24 * time.Hour

// MandateDecision is the result of evaluating whether a payment can proceed
// without an in-app Face ID step-up.
type MandateDecision struct {
	Allowed      bool
	RequiresAuth bool   // true when in-app Face ID is required
	AllowAuto    bool   // mandate permits fully-automatic recurring payments
	Reason       string // human-readable reason when RequiresAuth
}

// EvaluateMandate decides whether a payment of amountNGN in a category can run
// low-friction (poll/auto) under an active mandate, or needs in-app Face ID.
func (s *Service) EvaluateMandate(ctx context.Context, userID uuid.UUID, category string, amountNGN float64) (MandateDecision, error) {
	category = strings.ToLower(strings.TrimSpace(category))
	var (
		cap             float64
		daily           sql.NullFloat64
		allowAuto       bool
		stampedAt       time.Time
		mandateCategory string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT per_payment_cap_ngn, daily_cap_ngn, allow_auto, consent_stamped_at, product_category
		FROM bill_pay_mandates
		WHERE user_id=$1 AND status='active' AND product_category IN ($2, $3)
		ORDER BY (product_category = $2) DESC LIMIT 1`,
		userID, category, MandateAll).Scan(&cap, &daily, &allowAuto, &stampedAt, &mandateCategory)
	if err == sql.ErrNoRows {
		return MandateDecision{RequiresAuth: true, Reason: "no active mandate for this bill type"}, nil
	}
	if err != nil {
		return MandateDecision{}, err
	}
	if time.Since(stampedAt) > MandateReauthorizationWindow {
		return MandateDecision{RequiresAuth: true, Reason: "mandate consent expired; re-authorize in app"}, nil
	}
	if amountNGN > cap {
		return MandateDecision{RequiresAuth: true, Reason: fmt.Sprintf("amount ₦%.0f exceeds your ₦%.0f limit", amountNGN, cap)}, nil
	}
	if daily.Valid && daily.Float64 > 0 {
		// A blanket 'all' mandate counts daily spend across every bill category.
		scopeAll := mandateCategory == MandateAll
		spentToday, derr := s.spentToday(ctx, userID, category, scopeAll)
		if derr == nil && spentToday+amountNGN > daily.Float64 {
			return MandateDecision{RequiresAuth: true, Reason: "daily bill-pay limit reached"}, nil
		}
	}
	return MandateDecision{Allowed: true, AllowAuto: allowAuto}, nil
}

func (s *Service) spentToday(ctx context.Context, userID uuid.UUID, category string, allCategories bool) (float64, error) {
	var total sql.NullFloat64
	var query string
	var args []interface{}
	if allCategories {
		query = `SELECT COALESCE(SUM(amount_ngn),0) FROM airbills_orders
			WHERE user_id=$1 AND status NOT IN ('reversed','failed')
			AND created_at > NOW() - interval '24 hours'`
		args = []interface{}{userID}
	} else {
		query = `SELECT COALESCE(SUM(amount_ngn),0) FROM airbills_orders
			WHERE user_id=$1 AND product_category=$2 AND status NOT IN ('reversed','failed')
			AND created_at > NOW() - interval '24 hours'`
		args = []interface{}{userID, category}
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Float64, nil
}

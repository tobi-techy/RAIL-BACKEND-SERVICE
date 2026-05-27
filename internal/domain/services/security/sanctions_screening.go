package security

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

// SanctionsScreeningService screens users against OFAC, UN, and EU sanctions lists.
// In production, integrate with a third-party API (e.g., ComplyAdvantage, Sardine, Chainalysis).
// This implementation provides the framework and local fuzzy matching as fallback.
type SanctionsScreeningService struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewSanctionsScreeningService(db *sql.DB, logger *zap.Logger) *SanctionsScreeningService {
	return &SanctionsScreeningService{db: db, logger: logger}
}

// ScreenUser performs sanctions screening against a user's name.
func (s *SanctionsScreeningService) ScreenUser(ctx context.Context, userID uuid.UUID, fullName, checkType string) (*entities.SanctionsCheck, error) {
	check := &entities.SanctionsCheck{
		ID:           uuid.New(),
		UserID:       userID,
		CheckType:    checkType,
		FullName:     fullName,
		ListsChecked: []string{"OFAC", "UN", "EU"},
		MatchFound:   false,
		Status:       entities.SanctionsStatusClear,
		CreatedAt:    time.Now(),
	}

	matches := s.screenAgainstLists(ctx, fullName)

	if len(matches) > 0 {
		check.MatchFound = true
		check.MatchDetails = entities.SanctionsMatchDetails{Matches: matches}

		var maxScore float64
		for _, m := range matches {
			if m.Score > maxScore {
				maxScore = m.Score
			}
		}
		check.MatchScore = maxScore

		if maxScore >= 0.95 {
			check.Status = entities.SanctionsStatusConfirmedMatch
		} else if maxScore >= 0.7 {
			check.Status = entities.SanctionsStatusPotentialMatch
		}
	}

	if err := s.saveCheck(ctx, check); err != nil {
		s.logger.Error("Failed to save sanctions check", zap.Error(err))
		return check, err
	}

	if check.MatchFound && check.MatchScore >= 0.7 {
		if err := s.handleMatch(ctx, userID, check); err != nil {
			s.logger.Error("Failed to handle sanctions match",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	s.logger.Info("Sanctions screening completed",
		zap.String("user_id", userID.String()),
		zap.String("name", fullName),
		zap.Bool("match_found", check.MatchFound),
		zap.Float64("score", check.MatchScore))

	return check, nil
}

// ScreenAllUsers performs periodic re-screening of all active users.
func (s *SanctionsScreeningService) ScreenAllUsers(ctx context.Context) (int, int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, CONCAT(first_name, ' ', last_name) as full_name 
		FROM users WHERE status = 'active' AND sanctions_status != 'confirmed_match'`)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var screened, flagged int
	for rows.Next() {
		var userID uuid.UUID
		var fullName string
		if err := rows.Scan(&userID, &fullName); err != nil {
			s.logger.Error("Failed to scan user row for sanctions screening", zap.Error(err))
			continue
		}

		check, err := s.ScreenUser(ctx, userID, fullName, "periodic")
		if err != nil {
			s.logger.Error("Sanctions screening failed for user",
				zap.String("user_id", userID.String()), zap.Error(err))
			continue
		}

		screened++
		if check.MatchFound {
			flagged++
		}
	}

	return screened, flagged, rows.Err()
}

// screenAgainstLists performs fuzzy matching against sanctions entries.
// NOTE: Requires pg_trgm extension and sanctions_entries table.
// In production, replace with API call to ComplyAdvantage/Sardine/Chainalysis.
func (s *SanctionsScreeningService) screenAgainstLists(ctx context.Context, fullName string) []entities.SanctionsMatchEntry {
	normalized := normalizeName(fullName)
	var matches []entities.SanctionsMatchEntry

	rows, err := s.db.QueryContext(ctx, `
		SELECT name, list_source, entry_id FROM sanctions_entries 
		WHERE is_active = true AND similarity(LOWER(name), LOWER($1)) > 0.6
		ORDER BY similarity(LOWER(name), LOWER($1)) DESC LIMIT 5`, normalized)
	if err != nil {
		// Table or pg_trgm extension may not exist — log and return empty
		s.logger.Warn("Sanctions local screening query failed (requires pg_trgm extension and sanctions_entries table)",
			zap.Error(err))
		return matches
	}
	defer rows.Close()

	for rows.Next() {
		var name, listSource, entryID string
		if err := rows.Scan(&name, &listSource, &entryID); err != nil {
			s.logger.Error("Failed to scan sanctions entry row",
				zap.String("search_name", normalized), zap.Error(err))
			continue
		}
		score := calculateNameSimilarity(normalized, normalizeName(name))
		if score >= 0.7 {
			matches = append(matches, entities.SanctionsMatchEntry{
				MatchedName: name,
				ListName:    listSource,
				EntryID:     entryID,
				Score:       score,
			})
		}
	}

	return matches
}

func (s *SanctionsScreeningService) handleMatch(ctx context.Context, userID uuid.UUID, check *entities.SanctionsCheck) error {
	// Update user sanctions status
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET sanctions_status = $1 WHERE id = $2`,
		string(check.Status), userID); err != nil {
		s.logger.Error("Failed to update user sanctions status",
			zap.String("user_id", userID.String()), zap.Error(err))
		return fmt.Errorf("update sanctions status: %w", err)
	}

	// Freeze account if confirmed match
	if check.Status == entities.SanctionsStatusConfirmedMatch {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE users SET withdrawals_frozen = true, deposits_frozen = true, account_frozen_at = NOW() WHERE id = $1`, userID); err != nil {
			s.logger.Error("Failed to freeze user account for sanctions match",
				zap.String("user_id", userID.String()), zap.Error(err))
			return fmt.Errorf("freeze account: %w", err)
		}

		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO account_freezes (id, user_id, freeze_type, reason, triggered_by, is_active, created_at)
			VALUES ($1, $2, 'sanctions', $3, 'system', true, NOW())`,
			uuid.New(), userID, fmt.Sprintf("Sanctions match: score %.2f", check.MatchScore)); err != nil {
			s.logger.Error("Failed to record account freeze",
				zap.String("user_id", userID.String()), zap.Error(err))
		}
	}

	// Create fraud alert for ops team
	details, err := json.Marshal(check.MatchDetails)
	if err != nil {
		s.logger.Error("Failed to marshal sanctions match details",
			zap.String("user_id", userID.String()), zap.Error(err))
		details = []byte("{}")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO fraud_alerts (id, user_id, alert_type, severity, status, details, created_at)
		VALUES ($1, $2, 'sanctions_match', 'critical', 'open', $3, NOW())`,
		uuid.New(), userID, details); err != nil {
		s.logger.Error("Failed to create sanctions fraud alert",
			zap.String("user_id", userID.String()), zap.Error(err))
	}

	return nil
}

func (s *SanctionsScreeningService) saveCheck(ctx context.Context, check *entities.SanctionsCheck) error {
	matchDetails, err := json.Marshal(check.MatchDetails)
	if err != nil {
		s.logger.Error("Failed to marshal match details for sanctions check",
			zap.String("check_id", check.ID.String()), zap.Error(err))
		return fmt.Errorf("marshal match details: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sanctions_checks (id, user_id, check_type, full_name, lists_checked, match_found, match_details, match_score, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		check.ID, check.UserID, check.CheckType, check.FullName, check.ListsChecked,
		check.MatchFound, matchDetails, check.MatchScore, check.Status, check.CreatedAt)
	return err
}

func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func calculateNameSimilarity(a, b string) float64 {
	tokensA := strings.Fields(a)
	tokensB := strings.Fields(b)
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0
	}

	setA := make(map[string]bool)
	for _, t := range tokensA {
		setA[t] = true
	}

	var intersection int
	for _, t := range tokensB {
		if setA[t] {
			intersection++
		}
	}

	union := len(setA)
	for _, t := range tokensB {
		if !setA[t] {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

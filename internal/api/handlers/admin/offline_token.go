package admin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rail-service/rail_service/pkg/auth"
)

const (
	DefaultOfflineAdminTokenTTL = 15 * time.Minute
	MaxOfflineAdminTokenTTL     = 60 * time.Minute
)

// MintOfflineAdminToken is the operator's break-glass path when no admin has a
// usable password and second factor: run from a shell that already holds the
// deployment's database and signing credentials, it mints a short-lived access
// token for an existing, active admin or super_admin. It never runs over HTTP.
func MintOfflineAdminToken(ctx context.Context, store AdminCredentialStore, signingSecret, email string, ttl time.Duration) (string, *AdminCredential, time.Time, error) {
	if signingSecret == "" {
		return "", nil, time.Time{}, fmt.Errorf("signing secret is not configured")
	}
	if ttl <= 0 {
		ttl = DefaultOfflineAdminTokenTTL
	}
	if ttl > MaxOfflineAdminTokenTTL {
		return "", nil, time.Time{}, fmt.Errorf("ttl %s exceeds the maximum of %s", ttl, MaxOfflineAdminTokenTTL)
	}
	email = strings.ToLower(strings.TrimSpace(email))
	cred, err := store.FindAdminByEmail(ctx, email)
	if err != nil || cred == nil {
		return "", nil, time.Time{}, fmt.Errorf("no admin or super_admin account with that email")
	}
	if !cred.IsActive {
		return "", nil, time.Time{}, fmt.Errorf("account is inactive")
	}
	seconds := int(ttl.Seconds())
	pair, err := auth.NewJWTService(signingSecret, seconds, seconds, nil).GenerateTokenPairEnhanced(cred.ID, cred.Email, cred.Role)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return pair.AccessToken, cred, pair.AccessExpiresAt, nil
}

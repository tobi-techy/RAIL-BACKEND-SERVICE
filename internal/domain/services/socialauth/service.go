package socialauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/pkg/crypto"
)

type Config struct {
	Google OAuthConfig
	Apple  OAuthConfig
}

type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

type Service struct {
	db     *sql.DB
	logger *zap.Logger
	config Config
	client *http.Client
}

func NewService(db *sql.DB, logger *zap.Logger, config Config) *Service {
	return &Service{
		db:     db,
		logger: logger,
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetAuthURL generates OAuth authorization URL
func (s *Service) GetAuthURL(provider entities.SocialProvider, redirectURI, state string) (string, error) {
	switch provider {
	case entities.SocialProviderGoogle:
		return s.getGoogleAuthURL(redirectURI, state), nil
	case entities.SocialProviderApple:
		return s.getAppleAuthURL(redirectURI, state), nil
	default:
		return "", fmt.Errorf("unsupported provider: %s", provider)
	}
}

// Authenticate handles OAuth callback and returns user info
func (s *Service) Authenticate(ctx context.Context, req *entities.SocialLoginRequest) (*SocialUserInfo, error) {
	switch req.Provider {
	case entities.SocialProviderGoogle:
		return s.authenticateGoogle(ctx, req)
	case entities.SocialProviderApple:
		return s.authenticateApple(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", req.Provider)
	}
}

// LinkAccount links a social account to an existing user
func (s *Service) LinkAccount(ctx context.Context, userID uuid.UUID, info *SocialUserInfo) error {
	query := `
		INSERT INTO social_accounts (user_id, provider, provider_id, email, name, avatar_url, access_token, refresh_token, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (provider, provider_id) DO UPDATE SET
			email = EXCLUDED.email,
			name = EXCLUDED.name,
			avatar_url = EXCLUDED.avatar_url,
			access_token = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expires_at = EXCLUDED.expires_at,
			updated_at = NOW()`

	_, err := s.db.ExecContext(ctx, query,
		userID, info.Provider, info.ProviderID, info.Email, info.Name, info.AvatarURL,
		info.AccessToken, info.RefreshToken, info.ExpiresAt)
	if err != nil {
		return fmt.Errorf("failed to link social account: %w", err)
	}

	return nil
}

// GetLinkedAccounts returns all linked social accounts for a user
func (s *Service) GetLinkedAccounts(ctx context.Context, userID uuid.UUID) ([]entities.LinkedAccount, error) {
	query := `SELECT provider, email, name, created_at FROM social_accounts WHERE user_id = $1`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get linked accounts: %w", err)
	}
	defer rows.Close()

	var accounts []entities.LinkedAccount
	for rows.Next() {
		var acc entities.LinkedAccount
		var name sql.NullString
		if err := rows.Scan(&acc.Provider, &acc.Email, &name, &acc.LinkedAt); err != nil {
			return nil, fmt.Errorf("failed to scan account: %w", err)
		}
		if name.Valid {
			acc.Name = name.String
		}
		accounts = append(accounts, acc)
	}

	return accounts, nil
}

// UnlinkAccount removes a linked social account
func (s *Service) UnlinkAccount(ctx context.Context, userID uuid.UUID, provider entities.SocialProvider) error {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM social_accounts WHERE user_id = $1 AND provider = $2",
		userID, provider)
	if err != nil {
		return fmt.Errorf("failed to unlink account: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("account not found")
	}

	return nil
}

// FindUserByProvider finds a user by social provider ID
func (s *Service) FindUserByProvider(ctx context.Context, provider entities.SocialProvider, providerID string) (uuid.UUID, error) {
	var userID uuid.UUID
	err := s.db.QueryRowContext(ctx,
		"SELECT user_id FROM social_accounts WHERE provider = $1 AND provider_id = $2",
		provider, providerID).Scan(&userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return uuid.Nil, nil
		}
		return uuid.Nil, fmt.Errorf("failed to find user: %w", err)
	}
	return userID, nil
}

// SocialUserInfo represents user info from OAuth provider
type SocialUserInfo struct {
	Provider     entities.SocialProvider
	ProviderID   string
	Email        string
	Name         string
	AvatarURL    string
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
}

// Google OAuth
func (s *Service) getGoogleAuthURL(redirectURI, state string) string {
	params := url.Values{
		"client_id":     {s.config.Google.ClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

func (s *Service) authenticateGoogle(ctx context.Context, req *entities.SocialLoginRequest) (*SocialUserInfo, error) {
	// Exchange code for tokens
	tokenResp, err := s.exchangeGoogleCode(ctx, req.Code, req.RedirectURI)
	if err != nil {
		return nil, err
	}

	// Get user info
	userInfo, err := s.getGoogleUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, err
	}

	var expiresAt *time.Time
	if tokenResp.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		expiresAt = &t
	}

	return &SocialUserInfo{
		Provider:     entities.SocialProviderGoogle,
		ProviderID:   userInfo.ID,
		Email:        userInfo.Email,
		Name:         userInfo.Name,
		AvatarURL:    userInfo.Picture,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type googleUserInfo struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func (s *Service) exchangeGoogleCode(ctx context.Context, code, redirectURI string) (*googleTokenResponse, error) {
	data := url.Values{
		"code":          {code},
		"client_id":     {s.config.Google.ClientID},
		"client_secret": {s.config.Google.ClientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}

	resp, err := s.client.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	var tokenResp googleTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	return &tokenResp, nil
}

func (s *Service) getGoogleUserInfo(ctx context.Context, accessToken string) (*googleUserInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	defer resp.Body.Close()

	var userInfo googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return nil, fmt.Errorf("failed to decode user info: %w", err)
	}

	return &userInfo, nil
}

// Apple OAuth (simplified - Apple Sign In requires more setup)
func (s *Service) getAppleAuthURL(redirectURI, state string) string {
	params := url.Values{
		"client_id":     {s.config.Apple.ClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code id_token"},
		"scope":         {"name email"},
		"state":         {state},
		"response_mode": {"form_post"},
	}
	return "https://appleid.apple.com/auth/authorize?" + params.Encode()
}

func (s *Service) authenticateApple(ctx context.Context, req *entities.SocialLoginRequest) (*SocialUserInfo, error) {
	// Apple authentication requires JWT client secret generation
	// This is a simplified implementation
	if req.IDToken == "" {
		return nil, fmt.Errorf("Apple Sign In requires id_token")
	}

	// Decode ID token (in production, verify signature with Apple's public keys)
	claims, err := crypto.DecodeJWTClaims(req.IDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decode Apple ID token: %w", err)
	}

	email, _ := claims["email"].(string)
	sub, _ := claims["sub"].(string)

	return &SocialUserInfo{
		Provider:   entities.SocialProviderApple,
		ProviderID: sub,
		Email:      email,
	}, nil
}

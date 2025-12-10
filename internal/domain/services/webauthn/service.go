package webauthn

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/rail-service/rail_service/internal/domain/entities"
)

type Config struct {
	RPDisplayName string
	RPID          string
	RPOrigins     []string
}

type Service struct {
	db       *sql.DB
	logger   *zap.Logger
	webauthn *webauthn.WebAuthn
}

func NewService(db *sql.DB, logger *zap.Logger, config Config) (*Service, error) {
	wconfig := &webauthn.Config{
		RPDisplayName: config.RPDisplayName,
		RPID:          config.RPID,
		RPOrigins:     config.RPOrigins,
	}

	w, err := webauthn.New(wconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create webauthn: %w", err)
	}

	return &Service{
		db:       db,
		logger:   logger,
		webauthn: w,
	}, nil
}

// User implements webauthn.User interface
type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	Credentials []webauthn.Credential
}

func (u *User) WebAuthnID() []byte                         { return u.ID[:] }
func (u *User) WebAuthnName() string                       { return u.Email }
func (u *User) WebAuthnDisplayName() string                { return u.DisplayName }
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

// BeginRegistration starts WebAuthn registration
func (s *Service) BeginRegistration(ctx context.Context, userID uuid.UUID, email, displayName string) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	// Get existing credentials
	credentials, err := s.getCredentials(ctx, userID)
	if err != nil {
		return nil, nil, err
	}

	user := &User{
		ID:          userID,
		Email:       email,
		DisplayName: displayName,
		Credentials: credentials,
	}

	options, session, err := s.webauthn.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin registration: %w", err)
	}

	return options, session, nil
}

// FinishRegistration completes WebAuthn registration
func (s *Service) FinishRegistration(ctx context.Context, userID uuid.UUID, email, displayName, credName string, session *webauthn.SessionData, response *protocol.ParsedCredentialCreationData) error {
	user := &User{
		ID:          userID,
		Email:       email,
		DisplayName: displayName,
	}

	credential, err := s.webauthn.CreateCredential(user, *session, response)
	if err != nil {
		return fmt.Errorf("failed to create credential: %w", err)
	}

	// Store credential
	query := `
		INSERT INTO webauthn_credentials (user_id, credential_id, public_key, attestation_type, aaguid, sign_count, name)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err = s.db.ExecContext(ctx, query,
		userID,
		credential.ID,
		credential.PublicKey,
		credential.AttestationType,
		credential.Authenticator.AAGUID,
		credential.Authenticator.SignCount,
		credName,
	)
	if err != nil {
		return fmt.Errorf("failed to store credential: %w", err)
	}

	s.logger.Info("WebAuthn credential registered",
		zap.String("user_id", userID.String()),
		zap.String("name", credName))

	return nil
}

// BeginLogin starts WebAuthn login
func (s *Service) BeginLogin(ctx context.Context, userID uuid.UUID, email string) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	credentials, err := s.getCredentials(ctx, userID)
	if err != nil {
		return nil, nil, err
	}

	if len(credentials) == 0 {
		return nil, nil, fmt.Errorf("no credentials registered")
	}

	user := &User{
		ID:          userID,
		Email:       email,
		Credentials: credentials,
	}

	options, session, err := s.webauthn.BeginLogin(user)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin login: %w", err)
	}

	return options, session, nil
}

// FinishLogin completes WebAuthn login
func (s *Service) FinishLogin(ctx context.Context, userID uuid.UUID, email string, session *webauthn.SessionData, response *protocol.ParsedCredentialAssertionData) error {
	credentials, err := s.getCredentials(ctx, userID)
	if err != nil {
		return err
	}

	user := &User{
		ID:          userID,
		Email:       email,
		Credentials: credentials,
	}

	credential, err := s.webauthn.ValidateLogin(user, *session, response)
	if err != nil {
		return fmt.Errorf("failed to validate login: %w", err)
	}

	// Update sign count and last used
	_, err = s.db.ExecContext(ctx,
		"UPDATE webauthn_credentials SET sign_count = $1, last_used_at = NOW() WHERE credential_id = $2",
		credential.Authenticator.SignCount, credential.ID)
	if err != nil {
		s.logger.Warn("Failed to update credential sign count", zap.Error(err))
	}

	return nil
}

// GetCredentials returns user's WebAuthn credentials
func (s *Service) GetCredentials(ctx context.Context, userID uuid.UUID) ([]entities.WebAuthnCredentialInfo, error) {
	query := `SELECT id, name, created_at, last_used_at FROM webauthn_credentials WHERE user_id = $1`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	defer rows.Close()

	var creds []entities.WebAuthnCredentialInfo
	for rows.Next() {
		var c entities.WebAuthnCredentialInfo
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, fmt.Errorf("failed to scan credential: %w", err)
		}
		creds = append(creds, c)
	}

	return creds, nil
}

// DeleteCredential removes a WebAuthn credential
func (s *Service) DeleteCredential(ctx context.Context, userID, credentialID uuid.UUID) error {
	result, err := s.db.ExecContext(ctx,
		"DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2",
		credentialID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete credential: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("credential not found")
	}

	return nil
}

// FindUserByCredentialID finds user by credential ID (for discoverable credentials)
func (s *Service) FindUserByCredentialID(ctx context.Context, credentialID []byte) (uuid.UUID, error) {
	var userID uuid.UUID
	err := s.db.QueryRowContext(ctx,
		"SELECT user_id FROM webauthn_credentials WHERE credential_id = $1",
		credentialID).Scan(&userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return uuid.Nil, fmt.Errorf("credential not found")
		}
		return uuid.Nil, fmt.Errorf("failed to find user: %w", err)
	}
	return userID, nil
}

func (s *Service) getCredentials(ctx context.Context, userID uuid.UUID) ([]webauthn.Credential, error) {
	query := `SELECT credential_id, public_key, attestation_type, aaguid, sign_count FROM webauthn_credentials WHERE user_id = $1`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	defer rows.Close()

	var credentials []webauthn.Credential
	for rows.Next() {
		var c webauthn.Credential
		var aaguid []byte
		if err := rows.Scan(&c.ID, &c.PublicKey, &c.AttestationType, &aaguid, &c.Authenticator.SignCount); err != nil {
			return nil, fmt.Errorf("failed to scan credential: %w", err)
		}
		copy(c.Authenticator.AAGUID[:], aaguid)
		credentials = append(credentials, c)
	}

	return credentials, nil
}

// EncodeCredentialID encodes credential ID for JSON
func EncodeCredentialID(id []byte) string {
	return base64.RawURLEncoding.EncodeToString(id)
}

// DecodeCredentialID decodes credential ID from JSON
func DecodeCredentialID(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

package security

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type DeviceTrackingService struct {
	db     *sql.DB
	logger *zap.Logger
}

type KnownDevice struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Fingerprint   string
	DeviceName    string
	IPAddress     string
	Location      string
	IsTrusted     bool
	LastUsedAt    time.Time
	CreatedAt     time.Time
}

type DeviceCheckResult struct {
	IsKnownDevice   bool
	IsTrusted       bool
	RequiresVerify  bool
	Device          *KnownDevice
	RiskScore       float64
	RiskFactors     []string
}

func NewDeviceTrackingService(db *sql.DB, logger *zap.Logger) *DeviceTrackingService {
	return &DeviceTrackingService{db: db, logger: logger}
}

// GenerateFingerprint creates a device fingerprint from available data
func GenerateFingerprint(userAgent, acceptLanguage, screenRes, timezone string) string {
	data := fmt.Sprintf("%s|%s|%s|%s", userAgent, acceptLanguage, screenRes, timezone)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// CheckDevice verifies if device is known and trusted
func (s *DeviceTrackingService) CheckDevice(ctx context.Context, userID uuid.UUID, fingerprint, ipAddress string) (*DeviceCheckResult, error) {
	result := &DeviceCheckResult{
		RiskFactors: []string{},
	}

	// Look up device
	device, err := s.getDeviceByFingerprint(ctx, userID, fingerprint)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to check device: %w", err)
	}

	if device != nil {
		result.IsKnownDevice = true
		result.IsTrusted = device.IsTrusted
		result.Device = device

		// Check for IP change on known device
		if device.IPAddress != ipAddress {
			result.RiskFactors = append(result.RiskFactors, "ip_changed")
			result.RiskScore += 0.2
		}

		// Update last used
		s.updateDeviceLastUsed(ctx, device.ID, ipAddress)
	} else {
		result.IsKnownDevice = false
		result.RequiresVerify = true
		result.RiskFactors = append(result.RiskFactors, "new_device")
		result.RiskScore += 0.5
	}

	// Check for multiple devices in short time
	recentDevices, _ := s.countRecentDevices(ctx, userID, 24*time.Hour)
	if recentDevices > 3 {
		result.RiskFactors = append(result.RiskFactors, "multiple_devices")
		result.RiskScore += 0.3
	}

	return result, nil
}

// RegisterDevice adds a new device for a user
func (s *DeviceTrackingService) RegisterDevice(ctx context.Context, userID uuid.UUID, fingerprint, deviceName, ipAddress, location string) (*KnownDevice, error) {
	device := &KnownDevice{
		ID:          uuid.New(),
		UserID:      userID,
		Fingerprint: fingerprint,
		DeviceName:  deviceName,
		IPAddress:   ipAddress,
		Location:    location,
		IsTrusted:   false,
		LastUsedAt:  time.Now(),
		CreatedAt:   time.Now(),
	}

	query := `
		INSERT INTO known_devices (id, user_id, fingerprint, device_name, ip_address, location, is_trusted, last_used_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id, fingerprint) DO UPDATE SET
			last_used_at = EXCLUDED.last_used_at,
			ip_address = EXCLUDED.ip_address
		RETURNING id`

	err := s.db.QueryRowContext(ctx, query,
		device.ID, device.UserID, device.Fingerprint, device.DeviceName,
		device.IPAddress, device.Location, device.IsTrusted, device.LastUsedAt, device.CreatedAt,
	).Scan(&device.ID)

	if err != nil {
		return nil, fmt.Errorf("failed to register device: %w", err)
	}

	s.logger.Info("New device registered",
		zap.String("user_id", userID.String()),
		zap.String("device_id", device.ID.String()))

	return device, nil
}

// TrustDevice marks a device as trusted
func (s *DeviceTrackingService) TrustDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	query := `UPDATE known_devices SET is_trusted = true WHERE id = $1 AND user_id = $2`
	result, err := s.db.ExecContext(ctx, query, deviceID, userID)
	if err != nil {
		return fmt.Errorf("failed to trust device: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("device not found")
	}

	return nil
}

// RevokeDevice removes trust from a device
func (s *DeviceTrackingService) RevokeDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	query := `DELETE FROM known_devices WHERE id = $1 AND user_id = $2`
	_, err := s.db.ExecContext(ctx, query, deviceID, userID)
	return err
}

// GetUserDevices returns all devices for a user
func (s *DeviceTrackingService) GetUserDevices(ctx context.Context, userID uuid.UUID) ([]*KnownDevice, error) {
	query := `
		SELECT id, user_id, fingerprint, device_name, ip_address, location, is_trusted, last_used_at, created_at
		FROM known_devices WHERE user_id = $1 ORDER BY last_used_at DESC`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []*KnownDevice
	for rows.Next() {
		d := &KnownDevice{}
		err := rows.Scan(&d.ID, &d.UserID, &d.Fingerprint, &d.DeviceName, &d.IPAddress, &d.Location, &d.IsTrusted, &d.LastUsedAt, &d.CreatedAt)
		if err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}

	return devices, nil
}

func (s *DeviceTrackingService) getDeviceByFingerprint(ctx context.Context, userID uuid.UUID, fingerprint string) (*KnownDevice, error) {
	query := `
		SELECT id, user_id, fingerprint, device_name, ip_address, location, is_trusted, last_used_at, created_at
		FROM known_devices WHERE user_id = $1 AND fingerprint = $2`

	d := &KnownDevice{}
	err := s.db.QueryRowContext(ctx, query, userID, fingerprint).Scan(
		&d.ID, &d.UserID, &d.Fingerprint, &d.DeviceName, &d.IPAddress, &d.Location, &d.IsTrusted, &d.LastUsedAt, &d.CreatedAt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *DeviceTrackingService) updateDeviceLastUsed(ctx context.Context, deviceID uuid.UUID, ipAddress string) {
	query := `UPDATE known_devices SET last_used_at = NOW(), ip_address = $1 WHERE id = $2`
	s.db.ExecContext(ctx, query, ipAddress, deviceID)
}

func (s *DeviceTrackingService) countRecentDevices(ctx context.Context, userID uuid.UUID, window time.Duration) (int, error) {
	query := `SELECT COUNT(DISTINCT fingerprint) FROM known_devices WHERE user_id = $1 AND last_used_at > $2`
	var count int
	err := s.db.QueryRowContext(ctx, query, userID, time.Now().Add(-window)).Scan(&count)
	return count, err
}

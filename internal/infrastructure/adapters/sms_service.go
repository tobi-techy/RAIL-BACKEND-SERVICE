package adapters

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

// SMSConfig holds SMS service configuration
type SMSConfig struct {
	Provider    string // "twilio"
	APIKey      string
	APISecret   string
	FromNumber  string
	Environment string // "development", "staging", "production"
}

// SMSService implements SMS delivery interface
type SMSService struct {
	logger     *zap.Logger
	config     SMSConfig
	httpClient *http.Client
}

// NewSMSService creates a new SMS service
func NewSMSService(logger *zap.Logger, config SMSConfig) (*SMSService, error) {
	provider := strings.ToLower(strings.TrimSpace(config.Provider))
	if provider == "" {
		return nil, fmt.Errorf("sms provider is required")
	}

	switch provider {
	case "twilio":
		if strings.TrimSpace(config.APIKey) == "" {
			return nil, fmt.Errorf("twilio account sid is required")
		}
		if strings.TrimSpace(config.APISecret) == "" {
			return nil, fmt.Errorf("twilio auth token is required")
		}
		if strings.TrimSpace(config.FromNumber) == "" {
			return nil, fmt.Errorf("twilio from number is required")
		}
	default:
		return nil, fmt.Errorf("unsupported sms provider: %s", provider)
	}

	return &SMSService{
		logger:     logger,
		config:     config,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// SendVerificationSMS sends a verification code via SMS
func (s *SMSService) SendVerificationSMS(ctx context.Context, phone, code string) error {
	s.logger.Info("Sending verification SMS",
		zap.String("phone", s.maskPhone(phone)),
		zap.String("code", code))

	message := fmt.Sprintf("Your Stack verification code is: %s", code)
	if err := s.sendSMS(ctx, phone, message); err != nil {
		return fmt.Errorf("failed to send verification sms: %w", err)
	}

	return nil
}

// SendWelcomeSMS sends a welcome message via SMS
func (s *SMSService) SendWelcomeSMS(ctx context.Context, phone string) error {
	s.logger.Info("Sending welcome SMS",
		zap.String("phone", s.maskPhone(phone)))

	message := "Welcome to Stack! Your account is now active."
	if err := s.sendSMS(ctx, phone, message); err != nil {
		return fmt.Errorf("failed to send welcome sms: %w", err)
	}
	return nil
}

// SendKYCSMS sends KYC status update via SMS
func (s *SMSService) SendKYCSMS(ctx context.Context, phone, status string) error {
	s.logger.Info("Sending KYC status SMS",
		zap.String("phone", s.maskPhone(phone)),
		zap.String("status", status))

	var message string
	switch strings.ToLower(status) {
	case "approved":
		message = "Great news! Your KYC verification has been approved. You can now start investing."
	case "rejected":
		message = "Your KYC verification needs additional information. Please check your email for details."
	case "processing":
		message = "Your KYC verification is being processed. We'll notify you once it's complete."
	default:
		message = fmt.Sprintf("Your KYC status has been updated to: %s", status)
	}

	if err := s.sendSMS(ctx, phone, message); err != nil {
		return fmt.Errorf("failed to send kyc sms: %w", err)
	}

	return nil
}

// ValidatePhoneNumber validates phone number format (E.164)
func (s *SMSService) ValidatePhoneNumber(phone string) error {
	if phone == "" {
		return fmt.Errorf("phone number is required")
	}

	// Basic E.164 validation: starts with +, followed by 7-15 digits
	if len(phone) < 8 || len(phone) > 16 {
		return fmt.Errorf("phone number must be 8-16 characters")
	}

	if phone[0] != '+' {
		return fmt.Errorf("phone number must start with +")
	}

	// Check if remaining characters are digits
	for i := 1; i < len(phone); i++ {
		if phone[i] < '0' || phone[i] > '9' {
			return fmt.Errorf("phone number must contain only digits after +")
		}
	}

	return nil
}

// NormalizePhoneNumber normalizes phone number to E.164 format
func (s *SMSService) NormalizePhoneNumber(phone string) string {
	// Remove all non-digit characters except +
	normalized := "+"
	for _, char := range phone {
		if char >= '0' && char <= '9' {
			normalized += string(char)
		}
	}

	// If no + was found, add it
	if normalized == "+" {
		normalized = "+" + phone
	}

	return normalized
}

// maskPhone masks phone number for logging (e.g., +1234567890 -> +123****890)
func (s *SMSService) maskPhone(phone string) string {
	if len(phone) < 7 {
		return "****"
	}

	if len(phone) <= 4 {
		return phone[:2] + "****"
	}

	return phone[:3] + "****" + phone[len(phone)-3:]
}

func (s *SMSService) sendSMS(ctx context.Context, phone, message string) error {
	provider := strings.ToLower(strings.TrimSpace(s.config.Provider))
	switch provider {
	case "twilio":
		return s.sendTwilioSMS(ctx, phone, message)
	default:
		return fmt.Errorf("unsupported sms provider: %s", provider)
	}
}

func (s *SMSService) sendTwilioSMS(ctx context.Context, phone, message string) error {
	accountSID := strings.TrimSpace(s.config.APIKey)
	authToken := strings.TrimSpace(s.config.APISecret)
	normalized := s.NormalizePhoneNumber(phone)

	if err := s.ValidatePhoneNumber(normalized); err != nil {
		return fmt.Errorf("invalid phone number: %w", err)
	}

	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", accountSID)
	form := url.Values{}
	form.Set("To", normalized)
	form.Set("From", s.config.FromNumber)
	form.Set("Body", message)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("failed to build twilio request: %w", err)
	}
	req.SetBasicAuth(accountSID, authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send twilio sms: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		s.logger.Error("Twilio SMS send failed",
			zap.String("provider", "twilio"),
			zap.String("to", s.maskPhone(normalized)),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", string(bodyBytes)),
		)
		return fmt.Errorf("twilio sms send failed: status %d", resp.StatusCode)
	}

	s.logger.Info("SMS sent successfully",
		zap.String("provider", "twilio"),
		zap.String("to", s.maskPhone(normalized)),
		zap.Int("status_code", resp.StatusCode))

	return nil
}

// HealthCheck checks SMS service health
func (s *SMSService) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	provider := strings.ToLower(strings.TrimSpace(s.config.Provider))
	switch provider {
	case "twilio":
		return s.twilioHealthCheck(ctx)
	default:
		return fmt.Errorf("unsupported sms provider: %s", provider)
	}
}

func (s *SMSService) twilioHealthCheck(ctx context.Context) error {
	accountSID := strings.TrimSpace(s.config.APIKey)
	authToken := strings.TrimSpace(s.config.APISecret)
	endpoint := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s.json", accountSID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to build twilio health request: %w", err)
	}
	req.SetBasicAuth(accountSID, authToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("twilio health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("twilio health check error: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

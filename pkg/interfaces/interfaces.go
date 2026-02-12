package interfaces

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Core interfaces to break circular dependencies

// UserRepository defines user repository operations
type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*entities.UserProfile, error)
	GetByEmail(ctx context.Context, email string) (*entities.UserProfile, error)
	Create(ctx context.Context, user *entities.UserProfile) error
	Update(ctx context.Context, user *entities.UserProfile) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// WalletService defines wallet service operations
type WalletService interface {
	CreateWallet(ctx context.Context, userID uuid.UUID, chain entities.WalletChain) (*entities.Wallet, error)
	GetWallets(ctx context.Context, userID uuid.UUID) ([]*entities.Wallet, error)
	GetBalance(ctx context.Context, walletID string) (interface{}, error)
	SupportedChains() []entities.WalletChain
}

// KYCProvider defines KYC provider operations
type KYCProvider interface {
	SubmitKYC(ctx context.Context, submission interface{}) (interface{}, error)
	GetKYCStatus(ctx context.Context, userID uuid.UUID) (interface{}, error)
}

// AuditService defines audit service operations
type AuditService interface {
	LogEvent(ctx context.Context, event interface{}) error
	LogUserAction(ctx context.Context, userID uuid.UUID, action string, details map[string]interface{}) error
}

// EmailService defines email service operations
type EmailService interface {
	SendEmail(ctx context.Context, to, subject, body string) error
	SendVerificationEmail(ctx context.Context, email, code string) error
	SendPasswordResetEmail(ctx context.Context, email, token string) error
}

// SMSService defines SMS service operations
type SMSService interface {
	SendSMS(ctx context.Context, to, message string) error
	SendVerificationSMS(ctx context.Context, phone, code string) error
}

// FundingService defines funding service operations
type FundingService interface {
	ProcessDeposit(ctx context.Context, deposit *entities.Deposit) error
	InitiateWithdrawal(ctx context.Context, withdrawal *entities.Withdrawal) error
	GetDepositHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Deposit, error)
	GetWithdrawalHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Withdrawal, error)
}

// NotificationService defines notification service operations
type NotificationService interface {
	SendPushNotification(ctx context.Context, userID uuid.UUID, title, message string) error
	SendEmailNotification(ctx context.Context, userID uuid.UUID, subject, body string) error
	CreateNotification(ctx context.Context, notification *entities.Notification) error
	GetNotifications(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Notification, error)
	MarkAsRead(ctx context.Context, notificationID uuid.UUID, userID uuid.UUID) error
}

// CacheService defines cache service operations
type CacheService interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// EventPublisher defines event publishing operations
type EventPublisher interface {
	Publish(ctx context.Context, topic string, event interface{}) error
	PublishBatch(ctx context.Context, events []interface{}) error
}

// ServiceProvider provides access to all services
type ServiceProvider interface {
	GetUserRepository() UserRepository
	GetWalletService() WalletService
	GetKYCProvider() KYCProvider
	GetAuditService() AuditService
	GetEmailService() EmailService
	GetSMSService() SMSService
	GetFundingService() FundingService
	GetNotificationService() NotificationService
	GetCacheService() CacheService
	GetEventPublisher() EventPublisher
}

// ExternalServiceClient defines external service client interface
type ExternalServiceClient interface {
	Name() string
	IsHealthy(ctx context.Context) bool
	Call(ctx context.Context, method string, params map[string]interface{}) (interface{}, error)
}

// CircuitBreakerService defines circuit breaker operations
type CircuitBreakerService interface {
	Execute(service string, fn func() error) error
	IsOpen(service string) bool
	GetState(service string) string
	Reset(service string) error
}

// ValidationService defines validation operations
type ValidationService interface {
	ValidateRequest(req interface{}) error
	ValidateEmail(email string) error
	ValidatePhone(phone string) error
	ValidatePassword(password string) error
	ValidateAddress(address string, chain entities.WalletChain) error
	ValidateAmount(amount decimal.Decimal) error
}

// RateLimitService defines rate limiting operations
type RateLimitService interface {
	Allow(key string, limit int, window time.Duration) bool
	GetRemaining(key string, limit int, window time.Duration) int
	Reset(key string) error
}

// MetricsService defines metrics operations
type MetricsService interface {
	IncrementCounter(name string, tags map[string]string)
	RecordHistogram(name string, value float64, tags map[string]string)
	SetGauge(name string, value float64, tags map[string]string)
	RecordDuration(name string, duration time.Duration, tags map[string]string)
}

// LoggerService defines logging operations
type LoggerService interface {
	Debug(msg string, fields ...interface{})
	Info(msg string, fields ...interface{})
	Warn(msg string, fields ...interface{})
	Error(msg string, fields ...interface{})
	Fatal(msg string, fields ...interface{})
	With(fields ...interface{}) LoggerService
}

// DatabaseService defines database operations
type DatabaseService interface {
	BeginTx(ctx context.Context) (Transaction, error)
	Execute(ctx context.Context, query string, args ...interface{}) error
	Query(ctx context.Context, query string, args ...interface{}) (interface{}, error)
	Health() error
}

// Transaction defines database transaction operations
type Transaction interface {
	Commit() error
	Rollback() error
	Execute(ctx context.Context, query string, args ...interface{}) error
	Query(ctx context.Context, query string, args ...interface{}) (interface{}, error)
}

package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	Environment    string               `mapstructure:"environment"`
	LogLevel       string               `mapstructure:"log_level"`
	Server         ServerConfig         `mapstructure:"server"`
	RateLimit      RateLimitConfig      `mapstructure:"rate_limit"`
	Database       DatabaseConfig       `mapstructure:"database"`
	Redis          RedisConfig          `mapstructure:"redis"`
	JWT            JWTConfig            `mapstructure:"jwt"`
	Blockchain     BlockchainConfig     `mapstructure:"blockchain"`
	Payment        PaymentConfig        `mapstructure:"payment"`
	Security       SecurityConfig       `mapstructure:"security"`
	Circle         CircleConfig         `mapstructure:"circle"`
	KYC            KYCConfig            `mapstructure:"kyc"`
	Email          EmailConfig          `mapstructure:"email"`
	SMS            SMSConfig            `mapstructure:"sms"`
	Notification   NotificationConfig   `mapstructure:"notification"`
	Verification   VerificationConfig   `mapstructure:"verification"`
	Alpaca         AlpacaConfig         `mapstructure:"alpaca"`
	Bridge         BridgeConfig         `mapstructure:"bridge"`
	Grid           GridConfig           `mapstructure:"grid"`
	CCTP           CCTPConfig           `mapstructure:"cctp"`
	Workers        WorkerConfig         `mapstructure:"workers"`
	Reconciliation ReconciliationConfig `mapstructure:"reconciliation"`
	SocialAuth     SocialAuthConfig     `mapstructure:"social_auth"`
	WebAuthn       WebAuthnConfig       `mapstructure:"webauthn"`
	AI             AIConfig             `mapstructure:"ai"`
}

// GridConfig contains Grid API configuration
type GridConfig struct {
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
}

// CCTPConfig contains CCTP Iris API configuration
type CCTPConfig struct {
	BaseURL     string `mapstructure:"base_url"`
	Environment string `mapstructure:"environment"` // "sandbox" or "mainnet"
}

// AIConfig contains AI provider configuration
type AIConfig struct {
	OpenAI  OpenAIConfig `mapstructure:"openai"`
	Gemini  GeminiConfig `mapstructure:"gemini"`
	Primary string       `mapstructure:"primary"` // "openai" or "gemini"
}

// RateLimitConfig contains distributed rate limiting configuration
type RateLimitConfig struct {
	Enabled         bool                           `mapstructure:"enabled"`
	GlobalLimit     int64                          `mapstructure:"global_limit"`
	GlobalWindow    int                            `mapstructure:"global_window"` // Window in seconds
	IPLimit         int64                          `mapstructure:"ip_limit"`
	IPWindow        int                            `mapstructure:"ip_window"` // Window in seconds
	UserLimit       int64                          `mapstructure:"user_limit"`
	UserWindow      int                            `mapstructure:"user_window"` // Window in seconds
	EndpointLimits  map[string]EndpointLimitConfig `mapstructure:"endpoint_limits"`
	KeyPrefix       string                         `mapstructure:"key_prefix"`
	FailOpen        bool                           `mapstructure:"fail_open"` // Allow requests if Redis is unavailable
	ResponseHeaders bool                           `mapstructure:"response_headers"`
}

// EndpointLimitConfig contains rate limit for a specific endpoint
type EndpointLimitConfig struct {
	Limit  int64 `mapstructure:"limit"`
	Window int   `mapstructure:"window"` // Window in seconds
}

// OpenAIConfig contains OpenAI API configuration
type OpenAIConfig struct {
	APIKey      string  `mapstructure:"api_key"`
	Model       string  `mapstructure:"model"`
	MaxTokens   int     `mapstructure:"max_tokens"`
	Temperature float64 `mapstructure:"temperature"`
}

// GeminiConfig contains Google Gemini API configuration
type GeminiConfig struct {
	APIKey      string  `mapstructure:"api_key"`
	Model       string  `mapstructure:"model"`
	MaxTokens   int     `mapstructure:"max_tokens"`
	Temperature float64 `mapstructure:"temperature"`
}

type ServerConfig struct {
	Port              int      `mapstructure:"port"`
	Host              string   `mapstructure:"host"`
	ReadTimeout       int      `mapstructure:"read_timeout"`
	WriteTimeout      int      `mapstructure:"write_timeout"`
	AllowedOrigins    []string `mapstructure:"allowed_origins"`
	RateLimitPerMin   int      `mapstructure:"rate_limit_per_min"`
	SupportedVersions []string `mapstructure:"supported_versions"`
	DefaultVersion    string   `mapstructure:"default_version"`
	TrustedProxies    []string `mapstructure:"trusted_proxies"` // IPs of trusted reverse proxies for secure X-Forwarded-For handling
}

type DatabaseConfig struct {
	URL             string              `mapstructure:"url"`
	Host            string              `mapstructure:"host"`
	Port            int                 `mapstructure:"port"`
	Name            string              `mapstructure:"name"`
	User            string              `mapstructure:"user"`
	Password        string              `mapstructure:"password"`
	SSLMode         string              `mapstructure:"ssl_mode"`
	MaxOpenConns    int                 `mapstructure:"max_open_conns"`
	MaxIdleConns    int                 `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int                 `mapstructure:"conn_max_lifetime"`
	QueryTimeout    int                 `mapstructure:"query_timeout"`
	MaxRetries      int                 `mapstructure:"max_retries"`
	ReadReplicas    []ReadReplicaConfig `mapstructure:"read_replicas"`

	// Multi-region failover configuration
	PrimaryRegion   string `mapstructure:"primary_region"`
	FailoverEnabled bool   `mapstructure:"failover_enabled"`
}

type ReadReplicaConfig struct {
	Region string `mapstructure:"region"`
	Host   string `mapstructure:"host"`
	Port   int    `mapstructure:"port"`
	Name   string `mapstructure:"name"`
	User   string `mapstructure:"user"`
	Weight int    `mapstructure:"weight"` // Traffic distribution weight (0-100)
}

type RedisConfig struct {
	Host           string   `mapstructure:"host"`
	Port           int      `mapstructure:"port"`
	Password       string   `mapstructure:"password"`
	DB             int      `mapstructure:"db"`
	ClusterMode    bool     `mapstructure:"cluster_mode"`
	ClusterAddrs   []string `mapstructure:"cluster_addrs"`
	MaxRetries     int      `mapstructure:"max_retries"`
	PoolSize       int      `mapstructure:"pool_size"`
	MaxIdleConns   int      `mapstructure:"max_idle_conns"`
	MaxActiveConns int      `mapstructure:"max_active_conns"`
	IdleTimeout    int      `mapstructure:"idle_timeout"`
	RouteRandomly  bool     `mapstructure:"route_randomly"`
	RouteByLatency bool     `mapstructure:"route_by_latency"`
}

type JWTConfig struct {
	Secret     string `mapstructure:"secret"`
	AccessTTL  int    `mapstructure:"access_token_ttl"`
	RefreshTTL int    `mapstructure:"refresh_token_ttl"`
	Issuer     string `mapstructure:"issuer"`
}

type BlockchainConfig struct {
	Networks map[string]NetworkConfig `mapstructure:"networks"`
}

type NetworkConfig struct {
	Name           string                 `mapstructure:"name"`
	ChainID        int                    `mapstructure:"chain_id"`
	RPC            string                 `mapstructure:"rpc"`
	WebSocket      string                 `mapstructure:"websocket"`
	Explorer       string                 `mapstructure:"explorer"`
	NativeCurrency CurrencyConfig         `mapstructure:"native_currency"`
	Tokens         map[string]TokenConfig `mapstructure:"tokens"`
	GasLimit       int                    `mapstructure:"gas_limit"`
	MaxGasPrice    string                 `mapstructure:"max_gas_price"`
}

type CurrencyConfig struct {
	Name     string `mapstructure:"name"`
	Symbol   string `mapstructure:"symbol"`
	Decimals int    `mapstructure:"decimals"`
}

type TokenConfig struct {
	Address  string `mapstructure:"address"`
	Symbol   string `mapstructure:"symbol"`
	Name     string `mapstructure:"name"`
	Decimals int    `mapstructure:"decimals"`
	ChainID  int    `mapstructure:"chain_id"`
}

type PaymentConfig struct {
	ProcessorAPIKey string              `mapstructure:"processor_api_key"`
	WebhookSecret   string              `mapstructure:"webhook_secret"`
	Cards           CardProcessorConfig `mapstructure:"cards"`
	Supported       []string            `mapstructure:"supported_currencies"`
}

type CardProcessorConfig struct {
	Provider    string `mapstructure:"provider"`
	APIKey      string `mapstructure:"api_key"`
	APISecret   string `mapstructure:"api_secret"`
	WebhookURL  string `mapstructure:"webhook_url"`
	Environment string `mapstructure:"environment"` // sandbox, production
}

type SecurityConfig struct {
	EncryptionKey     string   `mapstructure:"encryption_key"`
	AllowedIPs        []string `mapstructure:"allowed_ips"`
	MaxLoginAttempts  int      `mapstructure:"max_login_attempts"`
	LockoutDuration   int      `mapstructure:"lockout_duration"`
	RequireMFA        bool     `mapstructure:"require_mfa"`
	PasswordMinLength int      `mapstructure:"password_min_length"`
	SessionTimeout    int      `mapstructure:"session_timeout"`

	// Enhanced security settings
	BcryptCost             int    `mapstructure:"bcrypt_cost"`              // bcrypt cost factor (12-14 recommended)
	PasswordHistoryCount   int    `mapstructure:"password_history_count"`   // number of passwords to track
	PasswordExpirationDays int    `mapstructure:"password_expiration_days"` // days until password expires (0=disabled)
	AccessTokenTTL         int    `mapstructure:"access_token_ttl"`         // short-lived access token TTL in seconds
	RefreshTokenTTL        int    `mapstructure:"refresh_token_ttl"`        // refresh token TTL in seconds
	EnableTokenBlacklist   bool   `mapstructure:"enable_token_blacklist"`   // enable token revocation
	CheckPasswordBreaches  bool   `mapstructure:"check_password_breaches"`  // check HaveIBeenPwned
	CaptchaThreshold       int    `mapstructure:"captcha_threshold"`        // failed attempts before CAPTCHA
	SecretsProvider        string `mapstructure:"secrets_provider"`         // "env", "aws_secrets_manager"
	AWSSecretsRegion       string `mapstructure:"aws_secrets_region"`       // AWS region for Secrets Manager
	AWSSecretsPrefix       string `mapstructure:"aws_secrets_prefix"`       // prefix for secret names
	SecretRotationDays     int    `mapstructure:"secret_rotation_days"`     // days between secret rotations

	// Admin creation security settings
	AdminBootstrapToken  string `mapstructure:"admin_bootstrap_token"`  // Required token for first admin creation
	DisableAdminCreation bool   `mapstructure:"disable_admin_creation"` // Completely disable admin creation endpoint

	// Device binding settings
	DeviceBinding DeviceBindingConfig `mapstructure:"device_binding"`

	// Webhook replay protection
	WebhookReplay WebhookReplayConfig `mapstructure:"webhook_replay"`

	// Adaptive rate limiting
	AdaptiveRateLimit AdaptiveRateLimitConfig `mapstructure:"adaptive_rate_limit"`
}

// DeviceBindingConfig for device-bound JWT tokens
type DeviceBindingConfig struct {
	Enabled               bool `mapstructure:"enabled"`
	MaxConcurrentSessions int  `mapstructure:"max_concurrent_sessions"`
	SessionTTLHours       int  `mapstructure:"session_ttl_hours"`
	StrictValidation      bool `mapstructure:"strict_validation"`
}

// WebhookReplayConfig for webhook replay protection
type WebhookReplayConfig struct {
	Enabled         bool `mapstructure:"enabled"`
	WindowSeconds   int  `mapstructure:"window_seconds"`
	MaxNonceAgeSecs int  `mapstructure:"max_nonce_age_seconds"`
}

// AdaptiveRateLimitConfig for risk-based rate limiting
type AdaptiveRateLimitConfig struct {
	Enabled           bool `mapstructure:"enabled"`
	EnableRiskScoring bool `mapstructure:"enable_risk_scoring"`
}

type CircleConfig struct {
	APIKey                 string   `mapstructure:"api_key"`
	Environment            string   `mapstructure:"environment"` // sandbox or production
	BaseURL                string   `mapstructure:"base_url"`
	EntitySecretCiphertext string   `mapstructure:"entity_secret_ciphertext"` // Pre-registered ciphertext from Circle Dashboard
	DefaultWalletSetID     string   `mapstructure:"default_wallet_set_id"`
	DefaultWalletSetName   string   `mapstructure:"default_wallet_set_name"`
	SupportedChains        []string `mapstructure:"supported_chains"`
}

type KYCConfig struct {
	Provider    string `mapstructure:"provider"` // "sumsub", "jumio"
	APIKey      string `mapstructure:"api_key"`
	APISecret   string `mapstructure:"api_secret"`
	BaseURL     string `mapstructure:"base_url"`
	CallbackURL string `mapstructure:"callback_url"`
	Environment string `mapstructure:"environment"` // "development", "sandbox", "production"
	UserAgent   string `mapstructure:"user_agent"`
	LevelName   string `mapstructure:"level_name"`
}

type EmailConfig struct {
	Provider     string `mapstructure:"provider"` // "sendgrid", "resend", "mailpit", "smtp"
	APIKey       string `mapstructure:"api_key"`
	FromEmail    string `mapstructure:"from_email"`
	FromName     string `mapstructure:"from_name"`
	BaseURL      string `mapstructure:"base_url"`    // For verification links
	Environment  string `mapstructure:"environment"` // "development", "staging", "production"
	ReplyTo      string `mapstructure:"reply_to"`
	SMTPHost     string `mapstructure:"smtp_host"`
	SMTPPort     int    `mapstructure:"smtp_port"`
	SMTPUsername string `mapstructure:"smtp_username"`
	SMTPPassword string `mapstructure:"smtp_password"`
	SMTPUseTLS   bool   `mapstructure:"smtp_use_tls"`
}

type SMSConfig struct {
	Provider    string `mapstructure:"provider"` // "twilio" or "sns"
	APIKey      string `mapstructure:"api_key"`
	APISecret   string `mapstructure:"api_secret"`
	FromNumber  string `mapstructure:"from_number"`
	Environment string `mapstructure:"environment"` // "development", "staging", "production"
}

// NotificationConfig contains AWS SNS/SQS notification configuration
type NotificationConfig struct {
	Provider             string `mapstructure:"provider"` // "sns" or "local"
	Region               string `mapstructure:"region"`
	PushPlatformARN      string `mapstructure:"push_platform_arn"`
	SMSTopicARN          string `mapstructure:"sms_topic_arn"`
	EmailTopicARN        string `mapstructure:"email_topic_arn"`
	NotificationQueueURL string `mapstructure:"notification_queue_url"`
}

type VerificationConfig struct {
	CodeLength       int `mapstructure:"code_length"`
	CodeTTLMinutes   int `mapstructure:"code_ttl_minutes"`
	MaxAttempts      int `mapstructure:"max_attempts"`
	RateLimitPerHour int `mapstructure:"rate_limit_per_hour"`
}

// BridgeConfig contains Bridge API configuration for wallets, virtual accounts, KYC, and cards
type BridgeConfig struct {
	APIKey          string   `mapstructure:"api_key"`
	BaseURL         string   `mapstructure:"base_url"`
	Environment     string   `mapstructure:"environment"`
	Timeout         int      `mapstructure:"timeout"`
	MaxRetries      int      `mapstructure:"max_retries"`
	SupportedChains []string `mapstructure:"supported_chains"`
	WebhookSecret   string   `mapstructure:"webhook_secret"`
}

// WorkerConfig contains background worker configuration
type WorkerConfig struct {
	Count      int `mapstructure:"count"`
	JobTimeout int `mapstructure:"job_timeout"`
}

// AlpacaConfig contains brokerage API configuration
type AlpacaConfig struct {
	ClientID      string `mapstructure:"client_id"`
	SecretKey     string `mapstructure:"secret_key"`
	BaseURL       string `mapstructure:"base_url"`
	DataBaseURL   string `mapstructure:"data_base_url"`   // Market data API base URL
	Environment   string `mapstructure:"environment"`     // sandbox or production
	Timeout       int    `mapstructure:"timeout"`         // Request timeout in seconds
	FirmAccountNo string `mapstructure:"firm_account_no"` // Firm account for instant funding
	WebhookSecret string `mapstructure:"webhook_secret"`  // Secret for verifying Alpaca webhooks
}

// ReconciliationConfig contains reconciliation service configuration
type ReconciliationConfig struct {
	Enabled                bool   `mapstructure:"enabled"`                   // Enable/disable reconciliation
	HourlyInterval         int    `mapstructure:"hourly_interval"`           // Interval in minutes for hourly runs
	DailyRunTime           string `mapstructure:"daily_run_time"`            // Time of day for daily run (HH:MM format)
	AutoCorrectLowSeverity bool   `mapstructure:"auto_correct_low_severity"` // Auto-correct <$1 discrepancies
	AlertWebhookURL        string `mapstructure:"alert_webhook_url"`         // Webhook URL for alerts
}

// SocialAuthConfig contains OAuth provider configuration
type SocialAuthConfig struct {
	Google OAuthProviderConfig      `mapstructure:"google"`
	Apple  AppleOAuthProviderConfig `mapstructure:"apple"`
}

// OAuthProviderConfig contains OAuth provider credentials
type OAuthProviderConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	RedirectURI  string `mapstructure:"redirect_uri"`
}

// AppleOAuthProviderConfig contains Apple Sign-In specific configuration
type AppleOAuthProviderConfig struct {
	ClientID    string `mapstructure:"client_id"`   // Bundle ID or Services ID
	TeamID      string `mapstructure:"team_id"`     // Apple Developer Team ID
	KeyID       string `mapstructure:"key_id"`      // Key ID from Apple Developer Portal
	PrivateKey  string `mapstructure:"private_key"` // P8 private key content (base64 encoded)
	RedirectURI string `mapstructure:"redirect_uri"`
}

// WebAuthnConfig contains WebAuthn/Passkey configuration
type WebAuthnConfig struct {
	RPDisplayName string   `mapstructure:"rp_display_name"` // Relying Party display name
	RPID          string   `mapstructure:"rp_id"`           // Relying Party ID (domain)
	RPOrigins     []string `mapstructure:"rp_origins"`      // Allowed origins
}

// ZeroGConfig contains configuration for 0G Network integration
type ZeroGConfig struct {
	// Storage configuration
	Storage ZeroGStorageConfig `mapstructure:"storage"`
	// Compute/Inference configuration
	Compute ZeroGComputeConfig `mapstructure:"compute"`
	// General settings
	Timeout        int  `mapstructure:"timeout"`          // Request timeout in seconds
	MaxRetries     int  `mapstructure:"max_retries"`      // Maximum retry attempts
	RetryBackoffMs int  `mapstructure:"retry_backoff_ms"` // Retry backoff in milliseconds
	EnableMetrics  bool `mapstructure:"enable_metrics"`   // Enable observability metrics
	EnableTracing  bool `mapstructure:"enable_tracing"`   // Enable distributed tracing
}

// ZeroGStorageConfig contains 0G storage specific configuration
type ZeroGStorageConfig struct {
	RPCEndpoint      string          `mapstructure:"rpc_endpoint"`      // 0G storage RPC endpoint
	IndexerRPC       string          `mapstructure:"indexer_rpc"`       // 0G indexer RPC endpoint
	PrivateKey       string          `mapstructure:"private_key"`       // Private key for storage operations
	MinReplicas      int             `mapstructure:"min_replicas"`      // Minimum replication count
	ExpectedReplicas int             `mapstructure:"expected_replicas"` // Expected replication count
	Namespaces       ZeroGNamespaces `mapstructure:"namespaces"`        // Storage namespaces
}

// ZeroGComputeConfig contains 0G compute/inference specific configuration
type ZeroGComputeConfig struct {
	BrokerEndpoint string           `mapstructure:"broker_endpoint"` // 0G compute broker endpoint
	PrivateKey     string           `mapstructure:"private_key"`     // Private key for compute operations
	ProviderID     string           `mapstructure:"provider_id"`     // Preferred inference provider ID
	ModelConfig    ZeroGModelConfig `mapstructure:"model_config"`    // AI model configuration
	Funding        ZeroGFunding     `mapstructure:"funding"`         // Account funding configuration
}

// ZeroGNamespaces contains predefined storage namespaces
type ZeroGNamespaces struct {
	AISummaries  string `mapstructure:"ai_summaries"`  // ai-summaries/ namespace
	AIArtifacts  string `mapstructure:"ai_artifacts"`  // ai-artifacts/ namespace
	ModelPrompts string `mapstructure:"model_prompts"` // model-prompts/ namespace
}

// ZeroGModelConfig contains AI model configuration
type ZeroGModelConfig struct {
	DefaultModel     string  `mapstructure:"default_model"`     // Default LLM model to use
	MaxTokens        int     `mapstructure:"max_tokens"`        // Maximum tokens per request
	Temperature      float64 `mapstructure:"temperature"`       // Model temperature setting
	TopP             float64 `mapstructure:"top_p"`             // Top-p sampling parameter
	FrequencyPenalty float64 `mapstructure:"frequency_penalty"` // Frequency penalty
	PresencePenalty  float64 `mapstructure:"presence_penalty"`  // Presence penalty
}

// ZeroGFunding contains account funding configuration
type ZeroGFunding struct {
	AutoTopup       bool    `mapstructure:"auto_topup"`        // Enable automatic balance top-up
	MinBalance      float64 `mapstructure:"min_balance"`       // Minimum account balance threshold
	TopupAmount     float64 `mapstructure:"topup_amount"`      // Amount to top up when threshold reached
	MaxAccountLimit float64 `mapstructure:"max_account_limit"` // Maximum account balance limit
}

// Load loads configuration from environment variables and config files
func Load() (*Config, error) {
	// Load .env file if it exists (ignore errors if file doesn't exist)
	godotenv.Load()

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath(".")

	// Set defaults
	setDefaults()

	// Read from config file if it exists
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Override with environment variables
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Override specific environment variables
	overrideFromEnv()

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	if strings.TrimSpace(config.Email.Provider) == "" && isDevEnvironment(config.Environment) {
		config.Email.Provider = "mailpit"
	}

	// Build database URL if not provided
	if config.Database.URL == "" {
		config.Database.URL = fmt.Sprintf(
			"postgres://%s:%s@%s:%d/%s?sslmode=%s",
			config.Database.User,
			config.Database.Password,
			config.Database.Host,
			config.Database.Port,
			config.Database.Name,
			config.Database.SSLMode,
		)
	}

	// Validate required fields
	if err := validate(&config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

func setDefaults() {
	// Server defaults
	viper.SetDefault("environment", "development")
	viper.SetDefault("log_level", "info")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.read_timeout", 30)
	viper.SetDefault("server.write_timeout", 30)
	viper.SetDefault("server.rate_limit_per_min", 100)
	viper.SetDefault("server.supported_versions", []string{"v1"})
	viper.SetDefault("server.default_version", "v1")

	// Database defaults
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.name", "stack_service")
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.ssl_mode", "disable")
	viper.SetDefault("database.max_open_conns", 25)      // Reduced for better resource management
	viper.SetDefault("database.max_idle_conns", 5)       // Reduced to prevent connection churn
	viper.SetDefault("database.conn_max_lifetime", 1800) // 30 minutes instead of 1 hour
	viper.SetDefault("database.query_timeout", 30)
	viper.SetDefault("database.max_retries", 3)

	// Redis defaults
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("redis.cluster_mode", false)
	viper.SetDefault("redis.max_retries", 3)
	viper.SetDefault("redis.pool_size", 10)

	// JWT defaults
	viper.SetDefault("jwt.access_token_ttl", 3600)   // 1 hour
	viper.SetDefault("jwt.refresh_token_ttl", 86400) // 24 hours
	viper.SetDefault("jwt.issuer", "stack_service")

	// Security defaults
	viper.SetDefault("security.max_login_attempts", 5)
	viper.SetDefault("security.lockout_duration", 900) // 15 minutes
	viper.SetDefault("security.require_mfa", false)
	viper.SetDefault("security.password_min_length", 8)

	// Circle defaults
	viper.SetDefault("circle.environment", "sandbox")
	viper.SetDefault("circle.api_key", "")
	viper.SetDefault("circle.base_url", "")
	viper.SetDefault("circle.default_wallet_set_id", "")
	viper.SetDefault("circle.default_wallet_set_name", "STACK-WalletSet")
	viper.SetDefault("circle.supported_chains", []string{"SOL-DEVNET"})

	// KYC defaults
	viper.SetDefault("kyc.provider", "")
	viper.SetDefault("kyc.environment", "development")
	viper.SetDefault("kyc.base_url", "https://netverify.com")
	viper.SetDefault("kyc.user_agent", "Stack-Service/1.0")
	viper.SetDefault("kyc.level_name", "basic-kyc")

	// Email defaults
	viper.SetDefault("email.provider", "")
	viper.SetDefault("email.from_email", "no-reply@stackservice.com")
	viper.SetDefault("email.from_name", "Stack Service")
	viper.SetDefault("email.environment", "development")
	viper.SetDefault("email.base_url", "http://localhost:3000")
	viper.SetDefault("email.reply_to", "")
	viper.SetDefault("email.smtp_host", "localhost")
	viper.SetDefault("email.smtp_port", 1025)
	viper.SetDefault("email.smtp_use_tls", false)

	// SMS defaults
	viper.SetDefault("sms.provider", "")
	viper.SetDefault("sms.environment", "development")

	// Verification defaults
	viper.SetDefault("verification.code_length", 6)
	viper.SetDefault("verification.code_ttl_minutes", 10)
	viper.SetDefault("verification.max_attempts", 3)
	viper.SetDefault("verification.rate_limit_per_hour", 3)

	viper.SetDefault("security.session_timeout", 3600) // 1 hour

	// Enhanced security defaults
	viper.SetDefault("security.bcrypt_cost", 12)                 // Increased from default 10
	viper.SetDefault("security.password_history_count", 5)       // Track last 5 passwords
	viper.SetDefault("security.password_expiration_days", 90)    // 90-day password expiration
	viper.SetDefault("security.access_token_ttl", 900)           // 15 minutes (short-lived)
	viper.SetDefault("security.refresh_token_ttl", 604800)       // 7 days
	viper.SetDefault("security.enable_token_blacklist", true)    // Enable token revocation
	viper.SetDefault("security.check_password_breaches", true)   // Check HaveIBeenPwned
	viper.SetDefault("security.captcha_threshold", 3)            // CAPTCHA after 3 failed attempts
	viper.SetDefault("security.secrets_provider", "env")         // Default to env vars
	viper.SetDefault("security.aws_secrets_region", "us-east-1") // Default AWS region
	viper.SetDefault("security.aws_secrets_prefix", "rail/")     // Prefix for secrets
	viper.SetDefault("security.secret_rotation_days", 90)        // 90-day rotation cycle

	// Device binding defaults
	viper.SetDefault("security.device_binding.enabled", true)
	viper.SetDefault("security.device_binding.max_concurrent_sessions", 3)
	viper.SetDefault("security.device_binding.session_ttl_hours", 24)
	viper.SetDefault("security.device_binding.strict_validation", true)

	// Webhook replay protection defaults
	viper.SetDefault("security.webhook_replay.enabled", true)
	viper.SetDefault("security.webhook_replay.window_seconds", 300)
	viper.SetDefault("security.webhook_replay.max_nonce_age_seconds", 300)

	// Adaptive rate limiting defaults
	viper.SetDefault("security.adaptive_rate_limit.enabled", true)
	viper.SetDefault("security.adaptive_rate_limit.enable_risk_scoring", true)

	// AI Provider defaults
	viper.SetDefault("ai.primary", "openai")
	viper.SetDefault("ai.openai.model", "gpt-4o-mini")
	viper.SetDefault("ai.openai.max_tokens", 500)
	viper.SetDefault("ai.openai.temperature", 0.7)
	viper.SetDefault("ai.gemini.model", "gemini-1.5-flash")
	viper.SetDefault("ai.gemini.max_tokens", 500)
	viper.SetDefault("ai.gemini.temperature", 0.7)

	// Compute defaults
	viper.SetDefault("zerog.compute.broker_endpoint", "")
	viper.SetDefault("zerog.compute.provider_id", "")
	viper.SetDefault("zerog.compute.model_config.default_model", "gpt-4")
	viper.SetDefault("zerog.compute.model_config.max_tokens", 4096)
	viper.SetDefault("zerog.compute.model_config.temperature", 0.7)
	viper.SetDefault("zerog.compute.model_config.top_p", 0.9)
	viper.SetDefault("zerog.compute.model_config.frequency_penalty", 0.0)
	viper.SetDefault("zerog.compute.model_config.presence_penalty", 0.0)
	viper.SetDefault("zerog.compute.funding.auto_topup", false)
	viper.SetDefault("zerog.compute.funding.min_balance", 10.0)
	viper.SetDefault("zerog.compute.funding.topup_amount", 50.0)
	viper.SetDefault("zerog.compute.funding.max_account_limit", 1000.0)

	// Alpaca defaults
	viper.SetDefault("alpaca.environment", "sandbox")
	viper.SetDefault("alpaca.base_url", "https://broker-api.sandbox.alpaca.markets")
	viper.SetDefault("alpaca.data_base_url", "https://data.sandbox.alpaca.markets")
	viper.SetDefault("alpaca.timeout", 30)

	// Bridge defaults
	viper.SetDefault("bridge.environment", "sandbox")
	viper.SetDefault("bridge.base_url", "https://api.bridge.xyz")
	viper.SetDefault("bridge.timeout", 30)
	viper.SetDefault("bridge.max_retries", 3)
	viper.SetDefault("bridge.supported_chains", []string{"ETH", "MATIC", "AVAX", "SOL"})

	// Worker defaults
	viper.SetDefault("workers.count", 10)
	viper.SetDefault("workers.job_timeout", 300)

	// Rate limiting defaults
	viper.SetDefault("rate_limit.enabled", true)
	viper.SetDefault("rate_limit.global_limit", 10000)
	viper.SetDefault("rate_limit.global_window", 60) // 1 minute
	viper.SetDefault("rate_limit.ip_limit", 500)
	viper.SetDefault("rate_limit.ip_window", 60) // 1 minute
	viper.SetDefault("rate_limit.user_limit", 200)
	viper.SetDefault("rate_limit.user_window", 60) // 1 minute
	viper.SetDefault("rate_limit.key_prefix", "ratelimit")
	viper.SetDefault("rate_limit.fail_open", true)
	viper.SetDefault("rate_limit.response_headers", true)

	// Endpoint-specific rate limits (per minute)
	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/login.limit", 5)
	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/login.window", 900) // 15 minutes

	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/register.limit", 3)
	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/register.window", 3600) // 1 hour

	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/funding/withdraw.limit", 10)
	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/funding/withdraw.window", 3600) // 1 hour

	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/forgot-password.limit", 3)
	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/forgot-password.window", 3600) // 1 hour

	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/reset-password.limit", 5)
	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/reset-password.window", 3600) // 1 hour

	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/resend-code.limit", 5)
	viper.SetDefault("rate_limit.endpoint_limits.POST:/api/v1/auth/resend-code.window", 900) // 15 minutes

	viper.SetDefault("rate_limit.endpoint_limits.GET:/api/v1/portfolio.limit", 60)
	viper.SetDefault("rate_limit.endpoint_limits.GET:/api/v1/portfolio.window", 60) // 1 minute

	viper.SetDefault("rate_limit.endpoint_limits.GET:/api/v1/balances.limit", 60)
	viper.SetDefault("rate_limit.endpoint_limits.GET:/api/v1/balances.window", 60) // 1 minute
}

func overrideFromEnv() {
	// Server
	if port := os.Getenv("PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			viper.Set("server.port", p)
		}
	}

	// Database
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		viper.Set("database.url", dbURL)
	}

	// JWT
	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		viper.Set("jwt.secret", jwtSecret)
	}

	// Encryption
	if encKey := os.Getenv("ENCRYPTION_KEY"); encKey != "" {
		viper.Set("security.encryption_key", encKey)
	}

	// Circle API
	if circleKey := os.Getenv("CIRCLE_API_KEY"); circleKey != "" {
		viper.Set("circle.api_key", circleKey)
	}
	if circleBaseURL := os.Getenv("CIRCLE_BASE_URL"); circleBaseURL != "" {
		viper.Set("circle.base_url", circleBaseURL)
	}
	// Load pre-registered entity secret ciphertext from environment
	if circleEntitySecretCiphertext := os.Getenv("CIRCLE_ENTITY_SECRET_CIPHERTEXT"); circleEntitySecretCiphertext != "" {
		viper.Set("circle.entity_secret_ciphertext", circleEntitySecretCiphertext)
	}
	if circleWalletSetID := os.Getenv("CIRCLE_DEFAULT_WALLET_SET_ID"); circleWalletSetID != "" {
		viper.Set("circle.default_wallet_set_id", circleWalletSetID)
	}
	if circleWalletSetName := os.Getenv("CIRCLE_DEFAULT_WALLET_SET_NAME"); circleWalletSetName != "" {
		viper.Set("circle.default_wallet_set_name", circleWalletSetName)
	}
	if supportedChains := os.Getenv("CIRCLE_SUPPORTED_CHAINS"); supportedChains != "" {
		parts := strings.Split(supportedChains, ",")
		var chains []string
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				chains = append(chains, strings.ToUpper(trimmed))
			}
		}
		if len(chains) > 0 {
			viper.Set("circle.supported_chains", chains)
		}
	}
	if circleEnv := os.Getenv("CIRCLE_ENVIRONMENT"); circleEnv != "" {
		viper.Set("circle.environment", circleEnv)
	}

	// KYC Provider
	if kycAPIKey := os.Getenv("KYC_API_KEY"); kycAPIKey != "" {
		viper.Set("kyc.api_key", kycAPIKey)
	}
	if sumsubToken := os.Getenv("SUMSUB_APP_TOKEN"); sumsubToken != "" {
		viper.Set("kyc.api_key", sumsubToken)
		viper.Set("kyc.provider", "sumsub")
	}
	if kycAPISecret := os.Getenv("KYC_API_SECRET"); kycAPISecret != "" {
		viper.Set("kyc.api_secret", kycAPISecret)
	}
	if sumsubSecret := os.Getenv("SUMSUB_SECRET_KEY"); sumsubSecret != "" {
		viper.Set("kyc.api_secret", sumsubSecret)
	}
	if kycProvider := os.Getenv("KYC_PROVIDER"); kycProvider != "" {
		viper.Set("kyc.provider", kycProvider)
	}
	if kycCallbackURL := os.Getenv("KYC_CALLBACK_URL"); kycCallbackURL != "" {
		viper.Set("kyc.callback_url", kycCallbackURL)
	}
	if kycBaseURL := os.Getenv("KYC_BASE_URL"); kycBaseURL != "" {
		viper.Set("kyc.base_url", kycBaseURL)
	}
	if sumsubBaseURL := os.Getenv("SUMSUB_BASE_URL"); sumsubBaseURL != "" {
		viper.Set("kyc.base_url", sumsubBaseURL)
	}
	if kycLevelName := os.Getenv("KYC_LEVEL_NAME"); kycLevelName != "" {
		viper.Set("kyc.level_name", kycLevelName)
	}
	if sumsubLevelName := os.Getenv("SUMSUB_LEVEL_NAME"); sumsubLevelName != "" {
		viper.Set("kyc.level_name", sumsubLevelName)
	}

	// Email Service
	if emailAPIKey := os.Getenv("EMAIL_API_KEY"); emailAPIKey != "" {
		viper.Set("email.api_key", emailAPIKey)
	}
	if resendAPIKey := os.Getenv("RESEND_API_KEY"); resendAPIKey != "" {
		viper.Set("email.api_key", resendAPIKey)
		viper.Set("email.provider", "resend")
	}
	if emailProvider := os.Getenv("EMAIL_PROVIDER"); emailProvider != "" {
		viper.Set("email.provider", emailProvider)
	}
	if baseURL := os.Getenv("BASE_URL"); baseURL != "" {
		viper.Set("email.base_url", baseURL)
	}
	if emailBaseURL := os.Getenv("EMAIL_BASE_URL"); emailBaseURL != "" {
		viper.Set("email.base_url", emailBaseURL)
	}
	if fromEmail := os.Getenv("EMAIL_FROM_EMAIL"); fromEmail != "" {
		viper.Set("email.from_email", fromEmail)
	}
	if resendFrom := os.Getenv("RESEND_FROM_EMAIL"); resendFrom != "" {
		viper.Set("email.from_email", resendFrom)
	}
	if fromName := os.Getenv("EMAIL_FROM_NAME"); fromName != "" {
		viper.Set("email.from_name", fromName)
	}
	if resendFromName := os.Getenv("RESEND_FROM_NAME"); resendFromName != "" {
		viper.Set("email.from_name", resendFromName)
	}
	if replyTo := os.Getenv("EMAIL_REPLY_TO"); replyTo != "" {
		viper.Set("email.reply_to", replyTo)
	}
	if smtpHost := os.Getenv("SMTP_HOST"); smtpHost != "" {
		viper.Set("email.smtp_host", smtpHost)
	}
	if smtpPort := os.Getenv("SMTP_PORT"); smtpPort != "" {
		viper.Set("email.smtp_port", smtpPort)
	}
	if smtpUser := os.Getenv("SMTP_USERNAME"); smtpUser != "" {
		viper.Set("email.smtp_username", smtpUser)
	}
	if smtpPass := os.Getenv("SMTP_PASSWORD"); smtpPass != "" {
		viper.Set("email.smtp_password", smtpPass)
	}

	// AI Providers
	if openaiKey := os.Getenv("OPENAI_API_KEY"); openaiKey != "" {
		viper.Set("ai.openai.api_key", openaiKey)
	}
	if geminiKey := os.Getenv("GEMINI_API_KEY"); geminiKey != "" {
		viper.Set("ai.gemini.api_key", geminiKey)
	}
	if aiPrimary := os.Getenv("AI_PRIMARY_PROVIDER"); aiPrimary != "" {
		viper.Set("ai.primary", aiPrimary)
	}

	// 0G Network
	// Storage configuration
	if zeroGStorageRPC := os.Getenv("ZEROG_STORAGE_RPC_ENDPOINT"); zeroGStorageRPC != "" {
		viper.Set("zerog.storage.rpc_endpoint", zeroGStorageRPC)
	}
	if zeroGIndexerRPC := os.Getenv("ZEROG_STORAGE_INDEXER_RPC"); zeroGIndexerRPC != "" {
		viper.Set("zerog.storage.indexer_rpc", zeroGIndexerRPC)
	}
	if zeroGStorageKey := os.Getenv("ZEROG_STORAGE_PRIVATE_KEY"); zeroGStorageKey != "" {
		viper.Set("zerog.storage.private_key", zeroGStorageKey)
	}

	// Compute configuration
	if zeroGComputeBroker := os.Getenv("ZEROG_COMPUTE_BROKER_ENDPOINT"); zeroGComputeBroker != "" {
		viper.Set("zerog.compute.broker_endpoint", zeroGComputeBroker)
	}
	if zeroGComputeKey := os.Getenv("ZEROG_COMPUTE_PRIVATE_KEY"); zeroGComputeKey != "" {
		viper.Set("zerog.compute.private_key", zeroGComputeKey)
	}
	if zeroGProviderID := os.Getenv("ZEROG_COMPUTE_PROVIDER_ID"); zeroGProviderID != "" {
		viper.Set("zerog.compute.provider_id", zeroGProviderID)
	}

	// Alpaca
	if alpacaAPIKey := os.Getenv("ALPACA_API_KEY"); alpacaAPIKey != "" {
		viper.Set("alpaca.client_id", alpacaAPIKey)
	}
	if alpacaAPISecret := os.Getenv("ALPACA_API_SECRET"); alpacaAPISecret != "" {
		viper.Set("alpaca.secret_key", alpacaAPISecret)
	}
	if alpacaBaseURL := os.Getenv("ALPACA_BASE_URL"); alpacaBaseURL != "" {
		viper.Set("alpaca.base_url", alpacaBaseURL)
	}
	if alpacaDataBaseURL := os.Getenv("ALPACA_DATA_BASE_URL"); alpacaDataBaseURL != "" {
		viper.Set("alpaca.data_base_url", alpacaDataBaseURL)
	}
	if alpacaEnvironment := os.Getenv("ALPACA_ENVIRONMENT"); alpacaEnvironment != "" {
		viper.Set("alpaca.environment", alpacaEnvironment)
	}
	if alpacaWebhookSecret := os.Getenv("ALPACA_WEBHOOK_SECRET"); alpacaWebhookSecret != "" {
		viper.Set("alpaca.webhook_secret", alpacaWebhookSecret)
	}

	// Bridge API
	if bridgeAPIKey := os.Getenv("BRIDGE_API_KEY"); bridgeAPIKey != "" {
		viper.Set("bridge.api_key", bridgeAPIKey)
	}
	if bridgeBaseURL := os.Getenv("BRIDGE_BASE_URL"); bridgeBaseURL != "" {
		viper.Set("bridge.base_url", bridgeBaseURL)
	}
	if bridgeEnvironment := os.Getenv("BRIDGE_ENVIRONMENT"); bridgeEnvironment != "" {
		viper.Set("bridge.environment", bridgeEnvironment)
	}
	if bridgeTimeout := os.Getenv("BRIDGE_TIMEOUT"); bridgeTimeout != "" {
		if timeout, err := strconv.Atoi(bridgeTimeout); err == nil {
			viper.Set("bridge.timeout", timeout)
		}
	}
	if bridgeMaxRetries := os.Getenv("BRIDGE_MAX_RETRIES"); bridgeMaxRetries != "" {
		if retries, err := strconv.Atoi(bridgeMaxRetries); err == nil {
			viper.Set("bridge.max_retries", retries)
		}
	}
	if bridgeSupportedChains := os.Getenv("BRIDGE_SUPPORTED_CHAINS"); bridgeSupportedChains != "" {
		parts := strings.Split(bridgeSupportedChains, ",")
		var chains []string
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				chains = append(chains, strings.ToUpper(trimmed))
			}
		}
		if len(chains) > 0 {
			viper.Set("bridge.supported_chains", chains)
		}
	}
	if bridgeWebhookSecret := os.Getenv("BRIDGE_WEBHOOK_SECRET"); bridgeWebhookSecret != "" {
		viper.Set("bridge.webhook_secret", bridgeWebhookSecret)
	}

	// Admin security settings
	if adminBootstrapToken := os.Getenv("ADMIN_BOOTSTRAP_TOKEN"); adminBootstrapToken != "" {
		viper.Set("security.admin_bootstrap_token", adminBootstrapToken)
	}
	if disableAdminCreation := os.Getenv("DISABLE_ADMIN_CREATION"); disableAdminCreation != "" {
		if disabled, err := strconv.ParseBool(disableAdminCreation); err == nil {
			viper.Set("security.disable_admin_creation", disabled)
		}
	}
}

func validate(config *Config) error {
	if config.JWT.Secret == "" {
		return fmt.Errorf("JWT secret is required")
	}

	if config.Security.EncryptionKey == "" {
		return fmt.Errorf("encryption key is required")
	}

	if config.Database.URL == "" && (config.Database.Host == "" || config.Database.Name == "") {
		return fmt.Errorf("database configuration is incomplete")
	}

	// Entity secret is now generated dynamically, no validation needed

	if len(config.Circle.SupportedChains) == 0 {
		return fmt.Errorf("circle supported chains configuration is required")
	}

	// Validate webhook secrets in production
	if config.Environment == "production" {
		if config.Bridge.WebhookSecret == "" {
			return fmt.Errorf("bridge webhook secret is required in production")
		}
		if config.Payment.WebhookSecret == "" {
			return fmt.Errorf("payment webhook secret is required in production")
		}
	}

	return nil
}

func isDevEnvironment(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "", "dev", "development", "local", "test", "testing":
		return true
	default:
		return false
	}
}

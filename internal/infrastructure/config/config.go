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
	Cache          CacheConfig          `mapstructure:"cache"`
	RateLimit      RateLimitConfig      `mapstructure:"rate_limit"`
	Database       DatabaseConfig       `mapstructure:"database"`
	Redis          RedisConfig          `mapstructure:"redis"`
	JWT            JWTConfig            `mapstructure:"jwt"`
	Blockchain     BlockchainConfig     `mapstructure:"blockchain"`
	Payment        PaymentConfig        `mapstructure:"payment"`
	Security       SecurityConfig       `mapstructure:"security"`
	Circle         CircleConfig         `mapstructure:"circle"`
	KYC            KYCConfig            `mapstructure:"kyc"`
	Cloudflare     CloudflareConfig     `mapstructure:"cloudflare"`
	Email          EmailConfig          `mapstructure:"email"`
	SMS            SMSConfig            `mapstructure:"sms"`
	Notification   NotificationConfig   `mapstructure:"notification"`
	Verification   VerificationConfig   `mapstructure:"verification"`
	Alpaca         AlpacaConfig         `mapstructure:"alpaca"`
	Bridge         BridgeConfig         `mapstructure:"bridge"`
	Blend          BlendConfig          `mapstructure:"blend"`
	Grid           GridConfig           `mapstructure:"grid"`
	CCTP           CCTPConfig           `mapstructure:"cctp"`
	ChainRails     ChainRailsConfig     `mapstructure:"chainrails"`
	Paj            PajConfig            `mapstructure:"paj"`
	RampHub        RampHubConfig        `mapstructure:"ramphub"`
	Workers        WorkerConfig         `mapstructure:"workers"`
	Reconciliation ReconciliationConfig `mapstructure:"reconciliation"`
	SocialAuth     SocialAuthConfig     `mapstructure:"social_auth"`
	WebAuthn       WebAuthnConfig       `mapstructure:"webauthn"`
	AI             AIConfig             `mapstructure:"ai"`
	SNSPush        SNSPushConfig        `mapstructure:"sns_push"`
	TelegramAlerts TelegramConfig       `mapstructure:"telegram_alerts"`
	Umbra          UmbraConfig          `mapstructure:"umbra"`
	Enrichment     EnrichmentConfig     `mapstructure:"enrichment"`
	Statement      StatementConfig      `mapstructure:"statement"`
}

// StatementConfig holds configuration for the bank statement processing pipeline.
type StatementConfig struct {
	S3Bucket       string `mapstructure:"s3_bucket"`        // S3 bucket for statement files (empty = store in DB)
	S3Prefix       string `mapstructure:"s3_prefix"`        // S3 key prefix (default: "statements/")
	S3Region       string `mapstructure:"s3_region"`        // AWS region for S3 bucket
	TextractRegion string `mapstructure:"textract_region"`  // AWS region for Textract (empty = disabled)
	EnableOCR      bool   `mapstructure:"enable_ocr"`       // Enable OCR fallback for scanned docs
	EnableVision   bool   `mapstructure:"enable_vision"`    // Enable vision LLM for images
	MaxFileSizeMB  int    `mapstructure:"max_file_size_mb"` // Max upload size (default: 20)
}

// TelegramConfig contains Telegram bot alerting configuration
type TelegramConfig struct {
	BotToken string `mapstructure:"bot_token"` // Telegram bot token from @BotFather
	ChatID   string `mapstructure:"chat_id"`   // Chat/group ID to send alerts to
}

// UmbraConfig contains Umbra privacy sidecar configuration
type UmbraConfig struct {
	SidecarURL string `mapstructure:"sidecar_url"` // URL of the Umbra sidecar service (e.g. http://localhost:3100)
	Enabled    bool   `mapstructure:"enabled"`     // Enable Umbra privacy shielding in allocation flow
	Network    string `mapstructure:"network"`     // mainnet or devnet
	AuthToken  string `mapstructure:"auth_token"`  // Shared secret for sidecar authentication
}

// EnrichmentConfig contains transaction enrichment sidecar configuration.
type EnrichmentConfig struct {
	ServiceURL string `mapstructure:"service_url"` // URL of the enrichment sidecar (e.g. http://enrichment:8090)
	Enabled    bool   `mapstructure:"enabled"`     // Enable transaction enrichment pipeline
}

// SNSPushConfig contains AWS SNS push notification configuration
type SNSPushConfig struct {
	Region             string `mapstructure:"region"`               // AWS region (defaults to app region)
	IOSPlatformARN     string `mapstructure:"ios_platform_arn"`     // SNS Platform Application ARN for APNs
	AndroidPlatformARN string `mapstructure:"android_platform_arn"` // SNS Platform Application ARN for FCM
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
	OpenAI      OpenAIConfig      `mapstructure:"openai"`
	Gemini      GeminiConfig      `mapstructure:"gemini"`
	Kimi        KimiConfig        `mapstructure:"kimi"`
	Groq        GroqConfig        `mapstructure:"groq"`
	Bedrock     BedrockConfig     `mapstructure:"bedrock"`
	AssemblyAI  AssemblyAIConfig  `mapstructure:"assemblyai"` // Deprecated: use ElevenLabs
	ElevenLabs  ElevenLabsConfig  `mapstructure:"elevenlabs"`
	Supermemory SupermemoryConfig `mapstructure:"supermemory"`
	Primary     string            `mapstructure:"primary"` // "openai", "gemini", "kimi", "groq", or "bedrock"
}

// SupermemoryConfig contains Supermemory API configuration.
type SupermemoryConfig struct {
	APIKey string `mapstructure:"api_key"`
}

// AssemblyAIConfig contains AssemblyAI Voice Agent API configuration (deprecated, kept for backward compat)
type AssemblyAIConfig struct {
	APIKey string `mapstructure:"api_key"`
	Voice  string `mapstructure:"voice"`
}

// ElevenLabsConfig contains ElevenLabs Conversational AI configuration
type ElevenLabsConfig struct {
	APIKey          string  `mapstructure:"api_key"`
	AgentID         string  `mapstructure:"agent_id"`          // ElevenLabs Conversational AI agent ID
	WebhookSecret   string  `mapstructure:"webhook_secret"`    // Secret for authenticating server tool webhook calls
	VoiceID         string  `mapstructure:"voice_id"`          // Optional: override agent's default voice
	PidginVoiceID   string  `mapstructure:"pidgin_voice_id"`   // Voice ID for Nigerian Pidgin speakers
	Stability       float64 `mapstructure:"stability"`         // 0-1: lower = more expressive but less stable
	SimilarityBoost float64 `mapstructure:"similarity_boost"`  // 0-1: how closely to match original voice
	Style           float64 `mapstructure:"style"`             // 0-1: style exaggeration
	UseSpeakerBoost bool    `mapstructure:"use_speaker_boost"` // enhance speaker clarity
}

// BedrockConfig contains Amazon Bedrock configuration
type BedrockConfig struct {
	Region           string  `mapstructure:"region"`
	ModelID          string  `mapstructure:"model_id"`
	MaxTokens        int     `mapstructure:"max_tokens"`
	Temperature      float64 `mapstructure:"temperature"`
	TopP             float64 `mapstructure:"top_p"`
	TimeoutSeconds   int     `mapstructure:"timeout_seconds"`
	GuardrailID      string  `mapstructure:"guardrail_id"`
	GuardrailVersion string  `mapstructure:"guardrail_version"`
}

// KimiConfig contains Kimi (Moonshot) API configuration
type KimiConfig struct {
	APIKey         string  `mapstructure:"api_key"`
	BaseURL        string  `mapstructure:"base_url"`
	Model          string  `mapstructure:"model"`
	MaxTokens      int     `mapstructure:"max_tokens"`
	Temperature    float64 `mapstructure:"temperature"`
	TopP           float64 `mapstructure:"top_p"`
	TimeoutSeconds int     `mapstructure:"timeout_seconds"`
	RateLimitRPM   int     `mapstructure:"rate_limit_rpm"`
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
	APIKey         string  `mapstructure:"api_key"`
	Model          string  `mapstructure:"model"`
	MaxTokens      int     `mapstructure:"max_tokens"`
	Temperature    float64 `mapstructure:"temperature"`
	TopP           float64 `mapstructure:"top_p"`
	TimeoutSeconds int     `mapstructure:"timeout_seconds"`
	RateLimitRPM   int     `mapstructure:"rate_limit_rpm"`
	RealtimeModel  string  `mapstructure:"realtime_model"`
}

// GeminiConfig contains Google Gemini API configuration
type GeminiConfig struct {
	APIKey         string  `mapstructure:"api_key"`
	Model          string  `mapstructure:"model"`
	MaxTokens      int     `mapstructure:"max_tokens"`
	Temperature    float64 `mapstructure:"temperature"`
	TopP           float64 `mapstructure:"top_p"`
	TimeoutSeconds int     `mapstructure:"timeout_seconds"`
	RateLimitRPM   int     `mapstructure:"rate_limit_rpm"`
}

// GroqConfig contains Groq API configuration (OpenAI-compatible)
type GroqConfig struct {
	APIKey         string  `mapstructure:"api_key"`
	Model          string  `mapstructure:"model"`
	MaxTokens      int     `mapstructure:"max_tokens"`
	Temperature    float64 `mapstructure:"temperature"`
	TopP           float64 `mapstructure:"top_p"`
	TimeoutSeconds int     `mapstructure:"timeout_seconds"`
	RateLimitRPM   int     `mapstructure:"rate_limit_rpm"`
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
	EnableGzip        bool     `mapstructure:"enable_gzip"`     // Enable gzip compression for responses
	EnableHTTP2       bool     `mapstructure:"enable_http2"`    // Enable HTTP/2 support
}

type CacheConfig struct {
	Enabled           bool `mapstructure:"enabled"`
	ProfileTTL        int  `mapstructure:"profile_ttl"`         // TTL for user profile cache (seconds)
	KYCStatusTTL      int  `mapstructure:"kyc_status_ttl"`      // TTL for KYC status cache (seconds)
	LimitsTTL         int  `mapstructure:"limits_ttl"`          // TTL for limits cache (seconds)
	PortfolioTTL      int  `mapstructure:"portfolio_ttl"`       // TTL for portfolio cache (seconds)
	BalancesTTL       int  `mapstructure:"balances_ttl"`        // TTL for balances cache (seconds)
	StationTTL        int  `mapstructure:"station_ttl"`         // TTL for station cache (seconds)
	InvalidateOnWrite bool `mapstructure:"invalidate_on_write"` // Invalidate cache on write operations
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
	TLS            bool     `mapstructure:"tls"`
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
	AuthBlacklistFailOpen  bool   `mapstructure:"auth_blacklist_fail_open"` // on Redis error, allow (true) vs 503 (false, default). Recently-seen tokens are served from the local cache regardless.
	CheckPasswordBreaches  bool   `mapstructure:"check_password_breaches"`  // check HaveIBeenPwned
	CaptchaThreshold       int    `mapstructure:"captcha_threshold"`        // failed attempts before CAPTCHA
	CaptchaSecretKey       string `mapstructure:"captcha_secret_key"`       // CAPTCHA provider secret key (e.g. reCAPTCHA)
	SecretsProvider        string `mapstructure:"secrets_provider"`         // "env", "aws_secrets_manager"
	AWSSecretsRegion       string `mapstructure:"aws_secrets_region"`       // AWS region for Secrets Manager
	AWSSecretsPrefix       string `mapstructure:"aws_secrets_prefix"`       // prefix for secret names
	SecretRotationDays     int    `mapstructure:"secret_rotation_days"`     // days between secret rotations

	// Admin creation security settings
	AdminBootstrapToken  string `mapstructure:"admin_bootstrap_token"`  // Required token for first admin creation
	DisableAdminCreation bool   `mapstructure:"disable_admin_creation"` // Completely disable admin creation endpoint

	// Device binding settings
	DeviceBinding DeviceBindingConfig `mapstructure:"device_binding"`

	// Internal API key for service-to-service auth
	InternalAPIKey string `mapstructure:"internal_api_key"`

	// InternalRequestSigningSecret, when set, requires HMAC-SHA256 request
	// signing on /internal/* endpoints in addition to the static API key
	// (defense-in-depth, TM-001). Empty = signing not enforced.
	InternalRequestSigningSecret string `mapstructure:"internal_request_signing_secret"`

	// AIFundActionsRequirePasscode requires a verified passcode session
	// (X-Passcode-Session) before the AI assistant can confirm a money-moving
	// action (withdrawal / stash transfer), mirroring the step-up the direct
	// withdrawal routes enforce (TM-004, TM-006). Default false so it can be
	// enabled once clients send the passcode-session header on AI confirms.
	AIFundActionsRequirePasscode bool `mapstructure:"ai_fund_actions_require_passcode"`

	// Webhook replay protection
	WebhookReplay WebhookReplayConfig `mapstructure:"webhook_replay"`

	// Adaptive rate limiting
	AdaptiveRateLimit AdaptiveRateLimitConfig `mapstructure:"adaptive_rate_limit"`

	// Webhook signature secrets for hardened verification (per-provider)
	WebhookSignatureSecrets WebhookSignatureSecretsConfig `mapstructure:"webhook_signature_secrets"`
}

// WebhookSignatureSecretsConfig holds per-provider webhook signing secrets
type WebhookSignatureSecretsConfig struct {
	Bridge string `mapstructure:"bridge"`
	Alpaca string `mapstructure:"alpaca"`
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

// CircleConfig holds configuration for Circle Programmable Wallets (primary wallet/custody provider).
type CircleConfig struct {
	APIKey                 string   `mapstructure:"api_key"`
	Environment            string   `mapstructure:"environment"` // sandbox or production
	BaseURL                string   `mapstructure:"base_url"`
	EntitySecret           string   `mapstructure:"entity_secret"`            // 64-char hex (32 bytes), stored securely
	EntitySecretCiphertext string   `mapstructure:"entity_secret_ciphertext"` // Pre-registered ciphertext from Circle Dashboard
	PublicKeyPEM           string   `mapstructure:"public_key_pem"`           // Circle public key for entity secret encryption
	DefaultWalletSetID     string   `mapstructure:"default_wallet_set_id"`
	DefaultWalletSetName   string   `mapstructure:"default_wallet_set_name"`
	SupportedChains        []string `mapstructure:"supported_chains"`
	TreasuryWalletAddress  string   `mapstructure:"treasury_wallet_address"`
	WebhookSecret          string   `mapstructure:"webhook_secret"`
	Timeout                int      `mapstructure:"timeout"`
	MaxRetries             int      `mapstructure:"max_retries"`
}

type KYCConfig struct {
	Provider      string `mapstructure:"provider"` // "didit" or legacy "sumsub"
	APIKey        string `mapstructure:"api_key"`
	APISecret     string `mapstructure:"api_secret"`
	WebhookSecret string `mapstructure:"webhook_secret"`
	BaseURL       string `mapstructure:"base_url"`
	CallbackURL   string `mapstructure:"callback_url"`
	Environment   string `mapstructure:"environment"` // "development", "sandbox", "production"
	UserAgent     string `mapstructure:"user_agent"`
	LevelName     string `mapstructure:"level_name"`
	WorkflowID    string `mapstructure:"workflow_id"` // Didit workflow ID

	// Didit-specific keys (isolated from Sumsub to avoid env var collisions)
	DiditAPIKey        string `mapstructure:"didit_api_key"`
	DiditWebhookSecret string `mapstructure:"didit_webhook_secret"`
	DiditWorkflowID    string `mapstructure:"didit_workflow_id"`

	// Enhanced KYC settings
	EnableLiveness      bool   `mapstructure:"enable_liveness"`       // Enable liveness detection
	EnableAMLCheck      bool   `mapstructure:"enable_aml_check"`      // Enable AML/PEP screening
	WatchlistProfile    string `mapstructure:"watchlist_profile"`     // "standard" or "enhanced"
	ReversionThreshold  int    `mapstructure:"reversion_threshold"`   // Days since last verification before reverify required
	HighValueWithdrawal int64  `mapstructure:"high_value_withdrawal"` // Amount that triggers reverification
}

type CloudflareConfig struct {
	AccountID   string `mapstructure:"account_id"`
	AccessKey   string `mapstructure:"access_key"`
	SecretKey   string `mapstructure:"secret_key"`
	R2Bucket    string `mapstructure:"r2_bucket"`
	R2PublicURL string `mapstructure:"r2_public_url"` // Custom domain for R2
	WorkerURL   string `mapstructure:"worker_url"`    // Cloudflare Worker URL for edge caching
	Proxied     bool   `mapstructure:"proxied"`       // True when all traffic is routed through Cloudflare
}

type EmailConfig struct {
	Provider    string `mapstructure:"provider"` // "ses", "resend", "unosend"
	APIKey      string `mapstructure:"api_key"`
	FromEmail   string `mapstructure:"from_email"`
	FromName    string `mapstructure:"from_name"`
	BaseURL     string `mapstructure:"base_url"`    // For verification links
	Environment string `mapstructure:"environment"` // "development", "staging", "production"
	ReplyTo     string `mapstructure:"reply_to"`
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
	APIKey                string   `mapstructure:"api_key"`
	BaseURL               string   `mapstructure:"base_url"`
	Environment           string   `mapstructure:"environment"`
	Timeout               int      `mapstructure:"timeout"`
	MaxRetries            int      `mapstructure:"max_retries"`
	SupportedChains       []string `mapstructure:"supported_chains"`
	WebhookSecret         string   `mapstructure:"webhook_secret"`
	TreasuryWalletAddress string   `mapstructure:"treasury_wallet_address"`
	// Rail's own Bridge custody account — used for reconciliation.
	RailCustomerID string `mapstructure:"rail_customer_id"`
}

// BlendConfig contains Blend.money non-custodial yield configuration.
// Blend deploys a per-user Gnosis Safe on Base and routes USDC through DeFi vaults.
type BlendConfig struct {
	Enabled            bool     `mapstructure:"enabled"`              // Master switch: route stash deposits into Blend yield
	APIKey             string   `mapstructure:"api_key"`              // Server-to-server X-API-Key
	BaseURL            string   `mapstructure:"base_url"`             // default: https://api.portal.blend.money
	AccountTypeID      string   `mapstructure:"account_type_id"`      // Blend account type slug (e.g. "savings-usd")
	ChainID            int64    `mapstructure:"chain_id"`             // 8453 = Base mainnet, 84532 = Base Sepolia
	USDCAddress        string   `mapstructure:"usdc_address"`         // USDC contract on the configured chain
	AllowedContracts   []string `mapstructure:"allowed_contracts"`    // EVM contracts Circle may sign calls to (REQUIRED in prod)
	BaseRPCURL         string   `mapstructure:"base_rpc_url"`         // Base JSON-RPC endpoint for on-chain Safe verification (REQUIRED in prod)
	RedeemTimeoutSecs  int      `mapstructure:"redeem_timeout_secs"`  // Max seconds to wait for a withdrawal to settle before failing
	WorkerIntervalSecs int      `mapstructure:"worker_interval_secs"` // Reconciliation worker tick interval
	WorkerBatchSize    int      `mapstructure:"worker_batch_size"`    // Routes processed per tick
}

// ChainRailsConfig contains ChainRails cross-chain deposit configuration.
type ChainRailsConfig struct {
	APIKey           string `mapstructure:"api_key"`
	WebhookSecret    string `mapstructure:"webhook_secret"`
	BaseURL          string `mapstructure:"base_url"`          // default: https://api.chainrails.io/api/v1
	DestinationChain string `mapstructure:"destination_chain"` // e.g. "SOLANA_MAINNET"
	SettlementToken  string `mapstructure:"settlement_token"`  // e.g. "USDC"
}

// PajConfig contains Paj Cash NGN on/off ramp configuration.
type PajConfig struct {
	APIKey        string `mapstructure:"api_key"`
	BaseURL       string `mapstructure:"base_url"`       // default: https://api.paj.cash
	WebhookURL    string `mapstructure:"webhook_url"`    // Rail's webhook endpoint URL (Paj posts per-order)
	WalletAddress string `mapstructure:"wallet_address"` // Rail's USDC custody wallet (onramp recipient)
	TokenMint     string `mapstructure:"token_mint"`     // USDC mint address on Solana
	Chain         string `mapstructure:"chain"`          // default: SOLANA
}

// RampHubConfig contains RampHub on/off ramp aggregator configuration.
type RampHubConfig struct {
	APIKey              string  `mapstructure:"api_key"`
	BaseURL             string  `mapstructure:"base_url"`              // default: https://api.ramphub.io
	WebhookSecret       string  `mapstructure:"webhook_secret"`        // HMAC-SHA256 signing secret for inbound webhooks
	WebhookURL          string  `mapstructure:"webhook_url"`           // Rail's webhook endpoint URL registered with RampHub
	DeveloperFeePercent float64 `mapstructure:"developer_fee_percent"` // Rail's business fee % applied to every order (e.g. 0.5)
}

// WorkerConfig contains background worker configuration
type WorkerConfig struct {
	Count                       int  `mapstructure:"count"`
	JobTimeout                  int  `mapstructure:"job_timeout"`
	MiriamIntelligenceLocal     bool `mapstructure:"miriam_intelligence_local"`
	MiriamIntelligenceBatchSize int  `mapstructure:"miriam_intelligence_batch_size"`
}

// AlpacaConfig contains brokerage API configuration
type AlpacaConfig struct {
	ClientID      string `mapstructure:"client_id"`
	SecretKey     string `mapstructure:"secret_key"`
	BaseURL       string `mapstructure:"base_url"`
	DataBaseURL   string `mapstructure:"data_base_url"`   // Market data API base URL
	DataAPIKey    string `mapstructure:"data_api_key"`    // Separate key for market data
	DataAPISecret string `mapstructure:"data_api_secret"` // Separate secret for market data
	DataFeed      string `mapstructure:"data_feed"`       // Preferred market data feed (iex, sip, otc)
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
		config.Email.Provider = "unosend"
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
	viper.SetDefault("server.enable_gzip", true)
	viper.SetDefault("server.enable_http2", true)

	// Cache defaults
	viper.SetDefault("cache.enabled", true)
	viper.SetDefault("cache.profile_ttl", 60)
	viper.SetDefault("cache.kyc_status_ttl", 30)
	viper.SetDefault("cache.limits_ttl", 300)
	viper.SetDefault("cache.portfolio_ttl", 10)
	viper.SetDefault("cache.balances_ttl", 30)
	viper.SetDefault("cache.station_ttl", 30)
	viper.SetDefault("cache.invalidate_on_write", true)

	// Database defaults - tuned for performance
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.name", "stack_service")
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.ssl_mode", "disable")
	viper.SetDefault("database.max_open_conns", 50)     // Increased for concurrent requests
	viper.SetDefault("database.max_idle_conns", 25)     // Keep more idle connections ready
	viper.SetDefault("database.conn_max_lifetime", 300) // 5 minutes - recycle connections more often
	viper.SetDefault("database.query_timeout", 30)
	viper.SetDefault("database.max_retries", 3)

	// Redis defaults
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("redis.tls", false)
	if redisTLS := os.Getenv("REDIS_TLS"); redisTLS != "" {
		viper.Set("redis.tls", redisTLS == "true")
	}
	viper.SetDefault("redis.cluster_mode", false)
	viper.SetDefault("redis.max_retries", 3)
	viper.SetDefault("redis.pool_size", 10)

	// JWT defaults
	viper.SetDefault("jwt.access_token_ttl", 3600)     // 1 hour
	viper.SetDefault("jwt.refresh_token_ttl", 2592000) // 30 days
	viper.SetDefault("jwt.issuer", "stack_service")

	// Security defaults
	viper.SetDefault("security.max_login_attempts", 5)
	viper.SetDefault("security.lockout_duration", 900) // 15 minutes
	viper.SetDefault("security.require_mfa", false)
	viper.SetDefault("security.password_min_length", 8)
	// Token-blacklist policy on Redis errors. Default STRICT (deny / 503) — the
	// secure choice for a fintech. Active sessions still ride through brief Redis
	// blips via the in-process negative cache; only cold tokens are denied during
	// a Redis outage. Set AUTH_BLACKLIST_FAIL_OPEN=true to prioritize
	// availability over revocation during an incident.
	viper.SetDefault("security.auth_blacklist_fail_open", false)
	viper.BindEnv("security.auth_blacklist_fail_open", "AUTH_BLACKLIST_FAIL_OPEN")

	// Payment/webhook defaults
	viper.SetDefault("payment.webhook_secret", "")

	// Circle defaults
	viper.SetDefault("circle.environment", "sandbox")
	viper.SetDefault("circle.api_key", "")
	viper.SetDefault("circle.base_url", "")
	viper.SetDefault("circle.default_wallet_set_id", "")
	viper.SetDefault("circle.default_wallet_set_name", "STACK-WalletSet")
	viper.SetDefault("circle.supported_chains", []string{"SOL", "ETH", "MATIC", "BASE", "AVAX", "ARB", "OP"})

	// KYC defaults
	viper.SetDefault("kyc.provider", "")
	viper.SetDefault("kyc.environment", "development")
	viper.SetDefault("kyc.webhook_secret", "")
	viper.SetDefault("kyc.base_url", "https://api.sumsub.com")
	viper.SetDefault("kyc.user_agent", "Stack-Service/1.0")
	viper.SetDefault("kyc.level_name", "basic-kyc")
	viper.SetDefault("kyc.enable_liveness", true)
	viper.SetDefault("kyc.enable_aml_check", false)
	viper.SetDefault("kyc.watchlist_profile", "standard")
	viper.SetDefault("kyc.reversion_threshold", 90)
	viper.SetDefault("kyc.high_value_withdrawal", 10000)

	// Cloudflare defaults
	viper.SetDefault("cloudflare.account_id", "")
	viper.SetDefault("cloudflare.access_key", "")
	viper.SetDefault("cloudflare.secret_key", "")
	viper.SetDefault("cloudflare.r2_bucket", "")
	viper.SetDefault("cloudflare.r2_public_url", "")
	viper.SetDefault("cloudflare.worker_url", "")

	// Email defaults
	viper.SetDefault("email.provider", "")
	viper.SetDefault("email.from_email", "no-reply@userail.money")
	viper.SetDefault("email.from_name", "RAIL")
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
	viper.SetDefault("security.access_token_ttl", 3600)          // 1 hour
	viper.SetDefault("security.refresh_token_ttl", 2592000)      // 30 days
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
	viper.SetDefault("ai.primary", "kimi")
	viper.SetDefault("ai.assemblyai.voice", "ivy")
	viper.SetDefault("ai.openai.model", "gpt-4o-mini")
	viper.SetDefault("ai.openai.realtime_model", "gpt-4o-mini-realtime-preview")
	viper.SetDefault("ai.openai.max_tokens", 500)
	viper.SetDefault("ai.openai.temperature", 0.7)
	viper.SetDefault("ai.gemini.model", "gemini-2.0-flash")
	viper.SetDefault("ai.gemini.max_tokens", 500)
	viper.SetDefault("ai.gemini.temperature", 0.7)
	viper.SetDefault("ai.kimi.model", "moonshot-v1-32k")

	// Explicit env bindings for AI keys (task def uses short names)
	viper.SetDefault("ai.elevenlabs.stability", 0.5)
	viper.SetDefault("ai.elevenlabs.similarity_boost", 0.75)
	viper.SetDefault("ai.elevenlabs.style", 0.0)
	viper.SetDefault("ai.elevenlabs.use_speaker_boost", true)
	viper.BindEnv("ai.assemblyai.api_key", "ASSEMBLYAI_API_KEY")
	viper.BindEnv("ai.assemblyai.voice", "ASSEMBLYAI_VOICE")
	viper.BindEnv("ai.elevenlabs.api_key", "ELEVENLABS_API_KEY")
	viper.BindEnv("ai.elevenlabs.agent_id", "ELEVENLABS_AGENT_ID")
	viper.BindEnv("ai.elevenlabs.webhook_secret", "ELEVENLABS_WEBHOOK_SECRET")
	viper.BindEnv("ai.elevenlabs.voice_id", "ELEVENLABS_VOICE_ID")
	viper.BindEnv("ai.elevenlabs.pidgin_voice_id", "ELEVENLABS_PIDGIN_VOICE_ID")
	viper.BindEnv("ai.supermemory.api_key", "SUPERMEMORY_API_KEY")
	viper.BindEnv("ai.elevenlabs.stability", "ELEVENLABS_STABILITY")
	viper.BindEnv("ai.elevenlabs.similarity_boost", "ELEVENLABS_SIMILARITY_BOOST")
	viper.BindEnv("ai.elevenlabs.style", "ELEVENLABS_STYLE")
	viper.BindEnv("ai.elevenlabs.use_speaker_boost", "ELEVENLABS_USE_SPEAKER_BOOST")
	viper.BindEnv("ai.openai.api_key", "OPENAI_API_KEY")
	viper.BindEnv("ai.gemini.api_key", "GEMINI_API_KEY")
	viper.BindEnv("ai.kimi.api_key", "KIMI_API_KEY")
	viper.BindEnv("ai.kimi.model", "KIMI_MODEL")
	viper.BindEnv("ai.groq.api_key", "GROQ_API_KEY")
	viper.BindEnv("ai.primary", "AI_PRIMARY")
	viper.BindEnv("ai.bedrock.region", "BEDROCK_REGION")
	viper.BindEnv("ai.bedrock.model_id", "BEDROCK_MODEL_ID")
	viper.BindEnv("ai.bedrock.guardrail_id", "BEDROCK_GUARDRAIL_ID")
	viper.BindEnv("ai.bedrock.guardrail_version", "BEDROCK_GUARDRAIL_VERSION")
	viper.SetDefault("ai.bedrock.model_id", "")
	viper.SetDefault("ai.bedrock.max_tokens", 4096)
	viper.SetDefault("ai.bedrock.temperature", 0.7)
	viper.SetDefault("ai.bedrock.top_p", 0.9)
	viper.SetDefault("ai.bedrock.timeout_seconds", 30)

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
	viper.SetDefault("alpaca.data_feed", "iex")
	viper.SetDefault("alpaca.timeout", 30)

	// Bridge defaults
	viper.SetDefault("bridge.environment", "production")
	viper.SetDefault("bridge.base_url", "https://api.bridge.xyz")
	viper.SetDefault("bridge.timeout", 30)

	// Blend.money defaults
	viper.SetDefault("blend.enabled", false)
	viper.SetDefault("blend.base_url", "https://api.portal.blend.money")
	viper.SetDefault("blend.chain_id", 8453) // Base mainnet
	viper.SetDefault("blend.usdc_address", "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
	viper.SetDefault("blend.base_rpc_url", "")
	viper.SetDefault("blend.redeem_timeout_secs", 180)
	viper.SetDefault("blend.worker_interval_secs", 30)
	viper.SetDefault("blend.worker_batch_size", 25)

	viper.SetDefault("bridge.max_retries", 3)
	viper.SetDefault("bridge.supported_chains", []string{"SOL", "MATIC", "CELO", "TRON", "BASE", "AVAX"})

	// Worker defaults
	viper.SetDefault("workers.count", 10)
	viper.SetDefault("workers.job_timeout", 300)
	viper.SetDefault("workers.miriam_intelligence_local", true)
	viper.SetDefault("workers.miriam_intelligence_batch_size", 500)

	// Rate limiting defaults
	viper.SetDefault("rate_limit.enabled", true)
	viper.SetDefault("rate_limit.global_limit", 10000)
	viper.SetDefault("rate_limit.global_window", 60) // 1 minute
	viper.SetDefault("rate_limit.ip_limit", 500)
	viper.SetDefault("rate_limit.ip_window", 60) // 1 minute
	viper.SetDefault("rate_limit.user_limit", 200)
	viper.SetDefault("rate_limit.user_window", 60) // 1 minute
	viper.SetDefault("rate_limit.key_prefix", "ratelimit")
	viper.SetDefault("rate_limit.fail_open", false)
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
	if corsOrigins := os.Getenv("CORS_ALLOWED_ORIGINS"); corsOrigins != "" {
		viper.Set("server.allowed_origins", strings.Split(corsOrigins, ","))
	}
	if trustedProxies := os.Getenv("TRUSTED_PROXIES"); trustedProxies != "" {
		viper.Set("server.trusted_proxies", strings.Split(trustedProxies, ","))
	}

	// WebAuthn
	if rpID := os.Getenv("WEBAUTHN_RP_ID"); rpID != "" {
		viper.Set("webauthn.rp_id", rpID)
	}
	if rpOrigins := os.Getenv("WEBAUTHN_RP_ORIGINS"); rpOrigins != "" {
		viper.Set("webauthn.rp_origins", strings.Split(rpOrigins, ","))
	}
	if rpDisplayName := os.Getenv("WEBAUTHN_RP_DISPLAY_NAME"); rpDisplayName != "" {
		viper.Set("webauthn.rp_display_name", rpDisplayName)
	}

	// SNS Push Notifications
	if v := os.Getenv("SNS_PUSH_REGION"); v != "" {
		viper.Set("sns_push.region", v)
	}
	if v := os.Getenv("SNS_PUSH_IOS_PLATFORM_ARN"); v != "" {
		viper.Set("sns_push.ios_platform_arn", v)
	}
	if v := os.Getenv("SNS_PUSH_ANDROID_PLATFORM_ARN"); v != "" {
		viper.Set("sns_push.android_platform_arn", v)
	}

	// Database
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		viper.Set("database.url", dbURL)
	}

	// JWT
	if jwtSecret := os.Getenv("JWT_SECRET"); jwtSecret != "" {
		viper.Set("jwt.secret", jwtSecret)
	}
	// Backward-compatible token TTL env vars used by existing deployments.
	if accessTokenTTL := os.Getenv("ACCESS_TOKEN_TTL"); accessTokenTTL != "" {
		if ttl, err := strconv.Atoi(accessTokenTTL); err == nil {
			viper.Set("jwt.access_token_ttl", ttl)
			viper.Set("security.access_token_ttl", ttl)
		}
	}
	if refreshTokenTTL := os.Getenv("REFRESH_TOKEN_TTL"); refreshTokenTTL != "" {
		if ttl, err := strconv.Atoi(refreshTokenTTL); err == nil {
			viper.Set("jwt.refresh_token_ttl", ttl)
			viper.Set("security.refresh_token_ttl", ttl)
		}
	}
	// Optional explicit split overrides.
	if jwtAccessTokenTTL := os.Getenv("JWT_ACCESS_TOKEN_TTL"); jwtAccessTokenTTL != "" {
		if ttl, err := strconv.Atoi(jwtAccessTokenTTL); err == nil {
			viper.Set("jwt.access_token_ttl", ttl)
		}
	}
	if jwtRefreshTokenTTL := os.Getenv("JWT_REFRESH_TOKEN_TTL"); jwtRefreshTokenTTL != "" {
		if ttl, err := strconv.Atoi(jwtRefreshTokenTTL); err == nil {
			viper.Set("jwt.refresh_token_ttl", ttl)
		}
	}
	if securityAccessTokenTTL := os.Getenv("SECURITY_ACCESS_TOKEN_TTL"); securityAccessTokenTTL != "" {
		if ttl, err := strconv.Atoi(securityAccessTokenTTL); err == nil {
			viper.Set("security.access_token_ttl", ttl)
		}
	}
	if securityRefreshTokenTTL := os.Getenv("SECURITY_REFRESH_TOKEN_TTL"); securityRefreshTokenTTL != "" {
		if ttl, err := strconv.Atoi(securityRefreshTokenTTL); err == nil {
			viper.Set("security.refresh_token_ttl", ttl)
		}
	}

	// Encryption
	if encKey := os.Getenv("ENCRYPTION_KEY"); encKey != "" {
		viper.Set("security.encryption_key", encKey)
	}

	// Legacy Circle API
	if circleKey := os.Getenv("CIRCLE_API_KEY"); circleKey != "" {
		viper.Set("circle.api_key", circleKey)
	}
	if circleBaseURL := os.Getenv("CIRCLE_BASE_URL"); circleBaseURL != "" {
		viper.Set("circle.base_url", circleBaseURL)
	}
	if circleEntitySecret := os.Getenv("CIRCLE_ENTITY_SECRET"); circleEntitySecret != "" {
		viper.Set("circle.entity_secret", circleEntitySecret)
	}
	// Load pre-registered entity secret ciphertext from environment
	if circleEntitySecretCiphertext := os.Getenv("CIRCLE_ENTITY_SECRET_CIPHERTEXT"); circleEntitySecretCiphertext != "" {
		viper.Set("circle.entity_secret_ciphertext", circleEntitySecretCiphertext)
	}
	// Load Circle public key PEM from environment for entity secret encryption.
	// Also accept the legacy EXPO_PUBLIC_ prefixed name for backwards compatibility.
	if circlePublicKeyPEM := os.Getenv("CIRCLE_PUBLIC_KEY_PEM"); circlePublicKeyPEM != "" {
		viper.Set("circle.public_key_pem", circlePublicKeyPEM)
	} else if circlePublicKeyPEM := os.Getenv("EXPO_PUBLIC_CIRCLE_PUBLIC_KEY_PEM"); circlePublicKeyPEM != "" {
		viper.Set("circle.public_key_pem", circlePublicKeyPEM)
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
	if treasuryWallet := os.Getenv("CIRCLE_TREASURY_WALLET_ADDRESS"); treasuryWallet != "" {
		viper.Set("circle.treasury_wallet_address", treasuryWallet)
	}
	if circleWebhookSec := os.Getenv("CIRCLE_WEBHOOK_SECRET"); circleWebhookSec != "" {
		viper.Set("circle.webhook_secret", circleWebhookSec)
	}
	if paymentWebhookSecret := os.Getenv("PAYMENT_WEBHOOK_SECRET"); paymentWebhookSecret != "" {
		viper.Set("payment.webhook_secret", paymentWebhookSecret)
	} else if circleWebhookSecret := os.Getenv("CIRCLE_WEBHOOK_SECRET"); circleWebhookSecret != "" {
		// Backward-compatible fallback used by existing deployments.
		viper.Set("payment.webhook_secret", circleWebhookSecret)
	}

	// KYC provider (Didit)
	if kycProvider := os.Getenv("KYC_PROVIDER"); kycProvider != "" {
		viper.Set("kyc.provider", kycProvider)
	}
	if diditAPIKey := os.Getenv("DIDIT_API_KEY"); diditAPIKey != "" {
		viper.Set("kyc.api_key", diditAPIKey)
		viper.Set("kyc.didit_api_key", diditAPIKey)
	}
	if diditWebhookSecret := os.Getenv("DIDIT_WEBHOOK_SECRET"); diditWebhookSecret != "" {
		viper.Set("kyc.webhook_secret", diditWebhookSecret)
		viper.Set("kyc.didit_webhook_secret", diditWebhookSecret)
	}
	if diditWorkflowID := os.Getenv("DIDIT_WORKFLOW_ID"); diditWorkflowID != "" {
		viper.Set("kyc.workflow_id", diditWorkflowID)
		viper.Set("kyc.didit_workflow_id", diditWorkflowID)
	}
	// Legacy Sumsub env vars (backward compat)
	if sumsubAppToken := os.Getenv("SUMSUB_APP_TOKEN"); sumsubAppToken != "" {
		viper.Set("kyc.api_key", sumsubAppToken)
	}
	if kycAPIKey := os.Getenv("KYC_API_KEY"); kycAPIKey != "" {
		viper.Set("kyc.api_key", kycAPIKey)
	}
	if sumsubSecretKey := os.Getenv("SUMSUB_SECRET_KEY"); sumsubSecretKey != "" {
		viper.Set("kyc.api_secret", sumsubSecretKey)
	}
	if kycAPISecret := os.Getenv("KYC_API_SECRET"); kycAPISecret != "" {
		viper.Set("kyc.api_secret", kycAPISecret)
	}
	if sumsubWebhookSecret := os.Getenv("SUMSUB_WEBHOOK_SECRET"); sumsubWebhookSecret != "" {
		viper.Set("kyc.webhook_secret", sumsubWebhookSecret)
	}
	if kycWebhookSecret := os.Getenv("KYC_WEBHOOK_SECRET"); kycWebhookSecret != "" {
		viper.Set("kyc.webhook_secret", kycWebhookSecret)
	}
	if sumsubBaseURL := os.Getenv("SUMSUB_BASE_URL"); sumsubBaseURL != "" {
		viper.Set("kyc.base_url", sumsubBaseURL)
	}
	if sumsubLevelName := os.Getenv("SUMSUB_LEVEL_NAME"); sumsubLevelName != "" {
		viper.Set("kyc.level_name", sumsubLevelName)
	}
	if sumsubEnvironment := os.Getenv("SUMSUB_ENVIRONMENT"); sumsubEnvironment != "" {
		viper.Set("kyc.environment", sumsubEnvironment)
	}

	// Email Service
	if resendAPIKey := os.Getenv("RESEND_API_KEY"); resendAPIKey != "" {
		viper.Set("email.api_key", resendAPIKey)
	}
	if unosendAPIKey := os.Getenv("UNOSEND_API_KEY"); unosendAPIKey != "" {
		viper.Set("email.api_key", unosendAPIKey)
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
	if fromName := os.Getenv("EMAIL_FROM_NAME"); fromName != "" {
		viper.Set("email.from_name", fromName)
	}
	if replyTo := os.Getenv("EMAIL_REPLY_TO"); replyTo != "" {
		viper.Set("email.reply_to", replyTo)
	}

	// AI Providers
	if assemblyAIKey := os.Getenv("ASSEMBLYAI_API_KEY"); assemblyAIKey != "" {
		viper.Set("ai.assemblyai.api_key", assemblyAIKey)
	} else {
		viper.Set("ai.assemblyai.api_key", "")
	}
	if assemblyAIVoice := os.Getenv("ASSEMBLYAI_VOICE"); assemblyAIVoice != "" {
		viper.Set("ai.assemblyai.voice", assemblyAIVoice)
	}
	if elevenLabsKey := os.Getenv("ELEVENLABS_API_KEY"); elevenLabsKey != "" {
		viper.Set("ai.elevenlabs.api_key", elevenLabsKey)
	} else {
		viper.Set("ai.elevenlabs.api_key", "")
	}
	if elevenLabsAgentID := os.Getenv("ELEVENLABS_AGENT_ID"); elevenLabsAgentID != "" {
		viper.Set("ai.elevenlabs.agent_id", elevenLabsAgentID)
	}
	if elevenLabsVoiceID := os.Getenv("ELEVENLABS_VOICE_ID"); elevenLabsVoiceID != "" {
		viper.Set("ai.elevenlabs.voice_id", elevenLabsVoiceID)
	}
	if elevenLabsStability := os.Getenv("ELEVENLABS_STABILITY"); elevenLabsStability != "" {
		if v, err := strconv.ParseFloat(elevenLabsStability, 64); err == nil {
			viper.Set("ai.elevenlabs.stability", v)
		}
	}
	if elevenLabsSimilarity := os.Getenv("ELEVENLABS_SIMILARITY_BOOST"); elevenLabsSimilarity != "" {
		if v, err := strconv.ParseFloat(elevenLabsSimilarity, 64); err == nil {
			viper.Set("ai.elevenlabs.similarity_boost", v)
		}
	}
	if elevenLabsStyle := os.Getenv("ELEVENLABS_STYLE"); elevenLabsStyle != "" {
		if v, err := strconv.ParseFloat(elevenLabsStyle, 64); err == nil {
			viper.Set("ai.elevenlabs.style", v)
		}
	}
	if elevenLabsSpeakerBoost := os.Getenv("ELEVENLABS_USE_SPEAKER_BOOST"); elevenLabsSpeakerBoost != "" {
		if v, err := strconv.ParseBool(elevenLabsSpeakerBoost); err == nil {
			viper.Set("ai.elevenlabs.use_speaker_boost", v)
		}
	}
	if openaiKey := os.Getenv("OPENAI_API_KEY"); openaiKey != "" {
		viper.Set("ai.openai.api_key", openaiKey)
	} else {
		viper.Set("ai.openai.api_key", "")
	}
	if geminiKey := os.Getenv("GEMINI_API_KEY"); geminiKey != "" {
		viper.Set("ai.gemini.api_key", geminiKey)
	} else {
		viper.Set("ai.gemini.api_key", "")
	}
	if kimiKey := os.Getenv("KIMI_API_KEY"); kimiKey != "" {
		viper.Set("ai.kimi.api_key", kimiKey)
	} else {
		viper.Set("ai.kimi.api_key", "")
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
	} else if apcaAPIKeyID := os.Getenv("APCA_API_KEY_ID"); apcaAPIKeyID != "" {
		viper.Set("alpaca.client_id", apcaAPIKeyID)
	}
	if alpacaAPISecret := os.Getenv("ALPACA_API_SECRET"); alpacaAPISecret != "" {
		viper.Set("alpaca.secret_key", alpacaAPISecret)
	} else if apcaAPISecretKey := os.Getenv("APCA_API_SECRET_KEY"); apcaAPISecretKey != "" {
		viper.Set("alpaca.secret_key", apcaAPISecretKey)
	}
	if alpacaDataAPIKey := os.Getenv("ALPACA_DATA_API_KEY"); alpacaDataAPIKey != "" {
		viper.Set("alpaca.data_api_key", alpacaDataAPIKey)
	} else if alpacaDataKey := os.Getenv("ALPACA_DATA_KEY"); alpacaDataKey != "" {
		viper.Set("alpaca.data_api_key", alpacaDataKey)
	}
	if alpacaDataAPISecret := os.Getenv("ALPACA_DATA_API_SECRET"); alpacaDataAPISecret != "" {
		viper.Set("alpaca.data_api_secret", alpacaDataAPISecret)
	} else if alpacaDataSecret := os.Getenv("ALPACA_DATA_SECRET"); alpacaDataSecret != "" {
		viper.Set("alpaca.data_api_secret", alpacaDataSecret)
	} else if alpacaDataAPISecretKey := os.Getenv("ALPACA_DATA_API_SECRET_KEY"); alpacaDataAPISecretKey != "" {
		viper.Set("alpaca.data_api_secret", alpacaDataAPISecretKey)
	}
	if alpacaBaseURL := os.Getenv("ALPACA_BASE_URL"); alpacaBaseURL != "" {
		viper.Set("alpaca.base_url", alpacaBaseURL)
	}
	if alpacaDataBaseURL := os.Getenv("ALPACA_DATA_BASE_URL"); alpacaDataBaseURL != "" {
		viper.Set("alpaca.data_base_url", alpacaDataBaseURL)
	}
	if alpacaDataFeed := os.Getenv("ALPACA_DATA_FEED"); alpacaDataFeed != "" {
		viper.Set("alpaca.data_feed", alpacaDataFeed)
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

	// ChainRails API
	viper.BindEnv("chainrails.api_key", "CHAINRAILS_API_KEY")
	viper.BindEnv("chainrails.webhook_secret", "CHAINRAILS_WEBHOOK_SECRET")
	viper.BindEnv("chainrails.base_url", "CHAINRAILS_BASE_URL")
	viper.BindEnv("chainrails.destination_chain", "CHAINRAILS_DESTINATION_CHAIN")
	viper.BindEnv("chainrails.settlement_token", "CHAINRAILS_SETTLEMENT_TOKEN")

	viper.BindEnv("security.internal_api_key", "SECURITY_INTERNAL_API_KEY")
	viper.BindEnv("workers.miriam_intelligence_local", "WORKERS_MIRIAM_INTELLIGENCE_LOCAL")
	viper.BindEnv("workers.miriam_intelligence_batch_size", "WORKERS_MIRIAM_INTELLIGENCE_BATCH_SIZE")

	if chainrailsAPIKey := os.Getenv("CHAINRAILS_API_KEY"); chainrailsAPIKey != "" {
		viper.Set("chainrails.api_key", chainrailsAPIKey)
	}
	if chainrailsWebhookSecret := os.Getenv("CHAINRAILS_WEBHOOK_SECRET"); chainrailsWebhookSecret != "" {
		viper.Set("chainrails.webhook_secret", chainrailsWebhookSecret)
	}
	if chainrailsBaseURL := os.Getenv("CHAINRAILS_BASE_URL"); chainrailsBaseURL != "" {
		viper.Set("chainrails.base_url", chainrailsBaseURL)
	}
	if chainrailsDestinationChain := os.Getenv("CHAINRAILS_DESTINATION_CHAIN"); chainrailsDestinationChain != "" {
		viper.Set("chainrails.destination_chain", chainrailsDestinationChain)
	}
	if chainrailsSettlementToken := os.Getenv("CHAINRAILS_SETTLEMENT_TOKEN"); chainrailsSettlementToken != "" {
		viper.Set("chainrails.settlement_token", chainrailsSettlementToken)
	}

	// Paj Cash NGN ramp
	viper.BindEnv("paj.api_key", "PAJ_API_KEY")
	viper.BindEnv("paj.base_url", "PAJ_BASE_URL")
	viper.BindEnv("paj.webhook_url", "PAJ_WEBHOOK_URL")
	viper.BindEnv("paj.wallet_address", "PAJ_WALLET_ADDRESS")
	viper.BindEnv("paj.token_mint", "PAJ_TOKEN_MINT")
	viper.BindEnv("paj.chain", "PAJ_CHAIN")

	for _, kv := range [][2]string{
		{"paj.api_key", "PAJ_API_KEY"},
		{"paj.base_url", "PAJ_BASE_URL"},
		{"paj.webhook_url", "PAJ_WEBHOOK_URL"},
		{"paj.wallet_address", "PAJ_WALLET_ADDRESS"},
		{"paj.token_mint", "PAJ_TOKEN_MINT"},
		{"paj.chain", "PAJ_CHAIN"},
	} {
		if v := os.Getenv(kv[1]); v != "" {
			viper.Set(kv[0], v)
		}
	}

	// RampHub on/off ramp aggregator
	for _, kv := range [][2]string{
		{"ramphub.api_key", "RAMPHUB_API_KEY"},
		{"ramphub.base_url", "RAMPHUB_BASE_URL"},
		{"ramphub.webhook_secret", "RAMPHUB_WEBHOOK_SECRET"},
		{"ramphub.webhook_url", "RAMPHUB_WEBHOOK_URL"},
		{"ramphub.developer_fee_percent", "RAMPHUB_DEVELOPER_FEE_PERCENT"},
	} {
		viper.BindEnv(kv[0], kv[1])
		if v := os.Getenv(kv[1]); v != "" {
			viper.Set(kv[0], v)
		}
	}

	// Apple Sign-In
	if v := os.Getenv("APPLE_TEAM_ID"); v != "" {
		viper.Set("social_auth.apple.team_id", v)
	}
	if v := os.Getenv("APPLE_KEY_ID"); v != "" {
		viper.Set("social_auth.apple.key_id", v)
	}
	if v := os.Getenv("APPLE_PRIVATE_KEY"); v != "" {
		viper.Set("social_auth.apple.private_key", v)
	}

	// Blend.money yield
	if v := os.Getenv("BLEND_ENABLED"); v != "" {
		viper.Set("blend.enabled", v)
	}
	if v := os.Getenv("BLEND_API_KEY"); v != "" {
		viper.Set("blend.api_key", v)
	}
	if v := os.Getenv("BLEND_BASE_URL"); v != "" {
		viper.Set("blend.base_url", v)
	}
	if v := os.Getenv("BLEND_ACCOUNT_TYPE_ID"); v != "" {
		viper.Set("blend.account_type_id", v)
	}
	if v := os.Getenv("BLEND_CHAIN_ID"); v != "" {
		viper.Set("blend.chain_id", v)
	}
	if v := os.Getenv("BLEND_USDC_ADDRESS"); v != "" {
		viper.Set("blend.usdc_address", v)
	}
	if v := os.Getenv("BLEND_BASE_RPC_URL"); v != "" {
		viper.Set("blend.base_rpc_url", v)
	}
	if v := os.Getenv("BLEND_ALLOWED_CONTRACTS"); v != "" {
		viper.Set("blend.allowed_contracts", splitCommaSeparated(v))
	}
	if v := os.Getenv("BLEND_REDEEM_TIMEOUT_SECS"); v != "" {
		viper.Set("blend.redeem_timeout_secs", v)
	}
	if v := os.Getenv("BLEND_WORKER_INTERVAL_SECS"); v != "" {
		viper.Set("blend.worker_interval_secs", v)
	}
	if v := os.Getenv("BLEND_WORKER_BATCH_SIZE"); v != "" {
		viper.Set("blend.worker_batch_size", v)
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

	// Telegram Alerts
	if v := os.Getenv("TELEGRAM_ALERTS_BOT_TOKEN"); v != "" {
		viper.Set("telegram_alerts.bot_token", v)
	}
	if v := os.Getenv("TELEGRAM_ALERTS_CHAT_ID"); v != "" {
		viper.Set("telegram_alerts.chat_id", v)
	}

	// Umbra Privacy Sidecar
	if v := os.Getenv("UMBRA_SIDECAR_URL"); v != "" {
		viper.Set("umbra.sidecar_url", v)
	}
	if v := os.Getenv("UMBRA_ENABLED"); v != "" {
		if enabled, err := strconv.ParseBool(v); err == nil {
			viper.Set("umbra.enabled", enabled)
		}
	}
	if v := os.Getenv("UMBRA_NETWORK"); v != "" {
		viper.Set("umbra.network", v)
	}
	if v := os.Getenv("UMBRA_SIDECAR_AUTH_TOKEN"); v != "" {
		viper.Set("umbra.auth_token", v)
	}
}

func validate(config *Config) error {
	if config.JWT.Secret == "" {
		return fmt.Errorf("JWT secret is required")
	}
	if len(config.JWT.Secret) < 32 {
		return fmt.Errorf("JWT secret must be at least 32 characters for adequate security")
	}

	if config.Security.EncryptionKey == "" {
		return fmt.Errorf("encryption key is required")
	}

	if config.Database.URL == "" && (config.Database.Host == "" || config.Database.Name == "") {
		return fmt.Errorf("database configuration is incomplete")
	}

	// Entity secret is now generated dynamically, no validation needed

	if len(config.Bridge.SupportedChains) == 0 {
		return fmt.Errorf("bridge supported chains configuration is required")
	}

	if err := validateBlendConfig(config); err != nil {
		return err
	}

	if config.RampHub.APIKey != "" {
		if config.RampHub.DeveloperFeePercent < 0 || config.RampHub.DeveloperFeePercent > 100 {
			return fmt.Errorf("ramphub.developer_fee_percent must be between 0 and 100, got %.4f", config.RampHub.DeveloperFeePercent)
		}
	}

	if strings.EqualFold(strings.TrimSpace(config.KYC.Provider), "sumsub") {
		if strings.TrimSpace(config.KYC.APIKey) == "" {
			return fmt.Errorf("sumsub app token is required when kyc.provider=sumsub")
		}
		if strings.TrimSpace(config.KYC.APISecret) == "" {
			return fmt.Errorf("sumsub secret key is required when kyc.provider=sumsub")
		}
		if strings.TrimSpace(config.KYC.BaseURL) == "" {
			return fmt.Errorf("sumsub base url is required when kyc.provider=sumsub")
		}
		if strings.TrimSpace(config.KYC.LevelName) == "" {
			return fmt.Errorf("sumsub level name is required when kyc.provider=sumsub")
		}
	}

	// Require Redis password in production
	if config.Environment == "production" {
		if config.Redis.Password == "" {
			return fmt.Errorf("redis password is required in %s environment", config.Environment)
		}
	}

	// Validate webhook secrets in production
	if config.Environment == "production" {
		if config.Bridge.WebhookSecret == "" {
			return fmt.Errorf("bridge webhook secret is required in production")
		}
		if strings.EqualFold(strings.TrimSpace(config.KYC.Provider), "sumsub") &&
			strings.TrimSpace(config.KYC.WebhookSecret) == "" {
			return fmt.Errorf("sumsub webhook secret is required in production when kyc.provider=sumsub")
		}
		if config.Payment.WebhookSecret == "" {
			return fmt.Errorf("payment webhook secret is required in production")
		}
	}

	return nil
}

func validateBlendConfig(config *Config) error {
	if !config.Blend.Enabled {
		return nil
	}
	if strings.TrimSpace(config.Blend.APIKey) == "" {
		return fmt.Errorf("blend api_key is required when blend.enabled is true")
	}
	if strings.TrimSpace(config.Blend.AccountTypeID) == "" {
		return fmt.Errorf("blend account_type_id is required when blend.enabled is true")
	}
	if config.Blend.ChainID == 0 {
		return fmt.Errorf("blend chain_id is required when blend.enabled is true")
	}
	if strings.TrimSpace(config.Blend.USDCAddress) == "" {
		return fmt.Errorf("blend usdc_address is required when blend.enabled is true")
	}
	// In production the contract allowlist is mandatory: signing arbitrary
	// Blend-supplied calldata without it would let a compromised session
	// route user funds to an attacker contract.
	if strings.TrimSpace(config.Blend.BaseRPCURL) == "" {
		return fmt.Errorf("BLEND_BASE_RPC_URL is required but not set")
	}
	if !isDevEnvironment(config.Environment) && len(config.Blend.AllowedContracts) == 0 {
		return fmt.Errorf("blend allowed_contracts must be configured in %s environment when blend.enabled is true", config.Environment)
	}
	// In production a Base RPC endpoint is mandatory so the per-user Safe (dynamically
	// trusted by the executor) is verified on-chain (contract + ownership) before any
	// call to it is signed. Without it the dynamic allow would be unverified.
	if !isDevEnvironment(config.Environment) && strings.TrimSpace(config.Blend.BaseRPCURL) == "" {
		return fmt.Errorf("blend base_rpc_url must be configured in %s environment when blend.enabled is true (e.g. https://mainnet.base.org)", config.Environment)
	}
	if !isDevEnvironment(config.Environment) {
		rpc := strings.TrimRight(strings.TrimSpace(config.Blend.BaseRPCURL), "/")
		for _, pub := range []string{"https://mainnet.base.org", "https://base.org"} {
			if strings.EqualFold(rpc, pub) {
				return fmt.Errorf("blend base_rpc_url cannot use public RPC endpoint %q in %s environment - use a dedicated provider (Alchemy, Infura, etc.)", rpc, config.Environment)
			}
		}
	}
	return nil
}

func isDevEnvironment(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "", "dev", "development", "local", "test", "testing", "staging":
		return true
	default:
		return false
	}
}

func splitCommaSeparated(v string) []string {
	parts := strings.Split(v, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

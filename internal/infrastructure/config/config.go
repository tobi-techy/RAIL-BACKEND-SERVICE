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
	Environment    string             `mapstructure:"environment"`
	LogLevel       string             `mapstructure:"log_level"`
	Server         ServerConfig       `mapstructure:"server"`
	Database       DatabaseConfig     `mapstructure:"database"`
	Redis          RedisConfig        `mapstructure:"redis"`
	JWT            JWTConfig          `mapstructure:"jwt"`
	Blockchain     BlockchainConfig   `mapstructure:"blockchain"`
	Payment        PaymentConfig      `mapstructure:"payment"`
	Security       SecurityConfig     `mapstructure:"security"`
	Circle         CircleConfig       `mapstructure:"circle"`
	KYC            KYCConfig          `mapstructure:"kyc"`
	Email          EmailConfig        `mapstructure:"email"`
	SMS            SMSConfig          `mapstructure:"sms"`
	Notification   NotificationConfig `mapstructure:"notification"`
	Verification   VerificationConfig `mapstructure:"verification"`
	Alpaca         AlpacaConfig         `mapstructure:"alpaca"`
	Bridge         BridgeConfig         `mapstructure:"bridge"`
	Workers        WorkerConfig         `mapstructure:"workers"`
	Reconciliation ReconciliationConfig `mapstructure:"reconciliation"`
	SocialAuth     SocialAuthConfig     `mapstructure:"social_auth"`
	WebAuthn       WebAuthnConfig       `mapstructure:"webauthn"`
	AI             AIConfig             `mapstructure:"ai"`
}

// AIConfig contains AI provider configuration
type AIConfig struct {
	OpenAI  OpenAIConfig  `mapstructure:"openai"`
	Gemini  GeminiConfig  `mapstructure:"gemini"`
	Primary string        `mapstructure:"primary"` // "openai" or "gemini"
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
}

type DatabaseConfig struct {
	URL             string   `mapstructure:"url"`
	Host            string   `mapstructure:"host"`
	Port            int      `mapstructure:"port"`
	Name            string   `mapstructure:"name"`
	User            string   `mapstructure:"user"`
	Password        string   `mapstructure:"password"`
	SSLMode         string   `mapstructure:"ssl_mode"`
	MaxOpenConns    int      `mapstructure:"max_open_conns"`
	MaxIdleConns    int      `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int      `mapstructure:"conn_max_lifetime"`
	QueryTimeout    int      `mapstructure:"query_timeout"`
	MaxRetries      int      `mapstructure:"max_retries"`
	ReadReplicas    []string `mapstructure:"read_replicas"`
}

type RedisConfig struct {
	Host         string   `mapstructure:"host"`
	Port         int      `mapstructure:"port"`
	Password     string   `mapstructure:"password"`
	DB           int      `mapstructure:"db"`
	ClusterMode  bool     `mapstructure:"cluster_mode"`
	ClusterAddrs []string `mapstructure:"cluster_addrs"`
	MaxRetries   int      `mapstructure:"max_retries"`
	PoolSize     int      `mapstructure:"pool_size"`
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
	BcryptCost              int    `mapstructure:"bcrypt_cost"`               // bcrypt cost factor (12-14 recommended)
	PasswordHistoryCount    int    `mapstructure:"password_history_count"`    // number of passwords to track
	PasswordExpirationDays  int    `mapstructure:"password_expiration_days"`  // days until password expires (0=disabled)
	AccessTokenTTL          int    `mapstructure:"access_token_ttl"`          // short-lived access token TTL in seconds
	RefreshTokenTTL         int    `mapstructure:"refresh_token_ttl"`         // refresh token TTL in seconds
	EnableTokenBlacklist    bool   `mapstructure:"enable_token_blacklist"`    // enable token revocation
	CheckPasswordBreaches   bool   `mapstructure:"check_password_breaches"`   // check HaveIBeenPwned
	CaptchaThreshold        int    `mapstructure:"captcha_threshold"`         // failed attempts before CAPTCHA
	SecretsProvider         string `mapstructure:"secrets_provider"`          // "env", "aws_secrets_manager"
	AWSSecretsRegion        string `mapstructure:"aws_secrets_region"`        // AWS region for Secrets Manager
	AWSSecretsPrefix        string `mapstructure:"aws_secrets_prefix"`        // prefix for secret names
	SecretRotationDays      int    `mapstructure:"secret_rotation_days"`      // days between secret rotations
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
}

// ReconciliationConfig contains reconciliation service configuration
type ReconciliationConfig struct {
	Enabled              bool   `mapstructure:"enabled"`                // Enable/disable reconciliation
	HourlyInterval       int    `mapstructure:"hourly_interval"`        // Interval in minutes for hourly runs
	DailyRunTime         string `mapstructure:"daily_run_time"`         // Time of day for daily run (HH:MM format)
	AutoCorrectLowSeverity bool `mapstructure:"auto_correct_low_severity"` // Auto-correct <$1 discrepancies
	AlertWebhookURL      string `mapstructure:"alert_webhook_url"`      // Webhook URL for alerts
}

// SocialAuthConfig contains OAuth provider configuration
type SocialAuthConfig struct {
	Google OAuthProviderConfig `mapstructure:"google"`
	Apple  OAuthProviderConfig `mapstructure:"apple"`
}

// OAuthProviderConfig contains OAuth provider credentials
type OAuthProviderConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	RedirectURI  string `mapstructure:"redirect_uri"`
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
	viper.SetDefault("database.max_open_conns", 100)
	viper.SetDefault("database.max_idle_conns", 25)
	viper.SetDefault("database.conn_max_lifetime", 3600)
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
	viper.SetDefault("jwt.access_token_ttl", 604800)   // 7 days
	viper.SetDefault("jwt.refresh_token_ttl", 2592000) // 30 days
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
	viper.SetDefault("security.bcrypt_cost", 12)                    // Increased from default 10
	viper.SetDefault("security.password_history_count", 5)          // Track last 5 passwords
	viper.SetDefault("security.password_expiration_days", 90)       // 90-day password expiration
	viper.SetDefault("security.access_token_ttl", 900)              // 15 minutes (short-lived)
	viper.SetDefault("security.refresh_token_ttl", 604800)          // 7 days
	viper.SetDefault("security.enable_token_blacklist", true)       // Enable token revocation
	viper.SetDefault("security.check_password_breaches", true)      // Check HaveIBeenPwned
	viper.SetDefault("security.captcha_threshold", 3)               // CAPTCHA after 3 failed attempts
	viper.SetDefault("security.secrets_provider", "env")            // Default to env vars
	viper.SetDefault("security.aws_secrets_region", "us-east-1")    // Default AWS region
	viper.SetDefault("security.aws_secrets_prefix", "rail/")        // Prefix for secrets
	viper.SetDefault("security.secret_rotation_days", 90)           // 90-day rotation cycle

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

	return nil
}

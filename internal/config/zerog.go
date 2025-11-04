package config

import (
	"time"
)

// ZeroGConfig holds configuration for 0G integration
type ZeroGConfig struct {
	Storage     StorageConfig     `yaml:"storage" mapstructure:"storage"`
	Compute     ComputeConfig     `yaml:"compute" mapstructure:"compute"`
	Scheduler   SchedulerConfig   `yaml:"scheduler" mapstructure:"scheduler"`
	HealthCheck HealthCheckConfig `yaml:"health_check" mapstructure:"health_check"`
}

// StorageConfig configures the 0G storage client
type StorageConfig struct {
	RPCEndpoint      string          `yaml:"rpc_endpoint" mapstructure:"rpc_endpoint"`
	IndexerRPC       string          `yaml:"indexer_rpc" mapstructure:"indexer_rpc"`
	PrivateKey       string          `yaml:"private_key" mapstructure:"private_key"`
	Timeout          time.Duration   `yaml:"timeout" mapstructure:"timeout"`
	MaxRetries       int             `yaml:"max_retries" mapstructure:"max_retries"`
	MinReplicas      int             `yaml:"min_replicas" mapstructure:"min_replicas"`
	ExpectedReplicas int             `yaml:"expected_replicas" mapstructure:"expected_replicas"`
	ChunkSize        int64           `yaml:"chunk_size" mapstructure:"chunk_size"`
	StorageClass     string          `yaml:"storage_class" mapstructure:"storage_class"`
	Namespaces       ZeroGNamespaces `yaml:"namespaces" mapstructure:"namespaces"`
}

// ZeroGNamespaces defines namespace configurations
type ZeroGNamespaces struct {
	AISummaries  string `yaml:"ai_summaries" mapstructure:"ai_summaries"`
	AIArtifacts  string `yaml:"ai_artifacts" mapstructure:"ai_artifacts"`
	ModelPrompts string `yaml:"model_prompts" mapstructure:"model_prompts"`
}

// ComputeConfig configures the 0G compute/inference client
type ComputeConfig struct {
	BrokerEndpoint string           `yaml:"broker_endpoint" mapstructure:"broker_endpoint"`
	PrivateKey     string           `yaml:"private_key" mapstructure:"private_key"`
	ProviderID     string           `yaml:"provider_id" mapstructure:"provider_id"`
	Timeout        time.Duration    `yaml:"timeout" mapstructure:"timeout"`
	MaxRetries     int              `yaml:"max_retries" mapstructure:"max_retries"`
	ModelConfig    ZeroGModelConfig `yaml:"model_config" mapstructure:"model_config"`
	Funding        ZeroGFunding     `yaml:"funding" mapstructure:"funding"`
}

// ZeroGModelConfig configures AI model settings
type ZeroGModelConfig struct {
	DefaultModel     string  `yaml:"default_model" mapstructure:"default_model"`
	MaxTokens        int     `yaml:"max_tokens" mapstructure:"max_tokens"`
	Temperature      float64 `yaml:"temperature" mapstructure:"temperature"`
	TopP             float64 `yaml:"top_p" mapstructure:"top_p"`
	FrequencyPenalty float64 `yaml:"frequency_penalty" mapstructure:"frequency_penalty"`
	PresencePenalty  float64 `yaml:"presence_penalty" mapstructure:"presence_penalty"`
}

// ZeroGFunding configures funding settings for compute operations
type ZeroGFunding struct {
	AutoTopup       bool    `yaml:"auto_topup" mapstructure:"auto_topup"`
	MinBalance      float64 `yaml:"min_balance" mapstructure:"min_balance"`
	TopupAmount     float64 `yaml:"topup_amount" mapstructure:"topup_amount"`
	MaxAccountLimit float64 `yaml:"max_account_limit" mapstructure:"max_account_limit"`
}

// SchedulerConfig configures the weekly summary scheduler
type SchedulerConfig struct {
	Enabled          bool   `yaml:"enabled" mapstructure:"enabled"`
	CronExpression   string `yaml:"cron_expression" mapstructure:"cron_expression"`
	BatchSize        int    `yaml:"batch_size" mapstructure:"batch_size"`
	ConcurrencyLimit int    `yaml:"concurrency_limit" mapstructure:"concurrency_limit"`
}

// HealthCheckConfig configures health check behavior
type HealthCheckConfig struct {
	Interval time.Duration `yaml:"interval" mapstructure:"interval"`
	Timeout  time.Duration `yaml:"timeout" mapstructure:"timeout"`
}

// DefaultZeroGConfig returns default configuration values
func DefaultZeroGConfig() *ZeroGConfig {
	return &ZeroGConfig{
		Storage: StorageConfig{
			RPCEndpoint:      "http://localhost:6789",
			IndexerRPC:       "http://localhost:6789",
			PrivateKey:       "", // Must be configured
			Timeout:          30 * time.Second,
			MaxRetries:       3,
			MinReplicas:      1,
			ExpectedReplicas: 3,
			ChunkSize:        2 * 1024 * 1024, // 2MB chunks
			StorageClass:     "standard",
			Namespaces: ZeroGNamespaces{
				AISummaries:  "ai-summaries/",
				AIArtifacts:  "ai-artifacts/",
				ModelPrompts: "model-prompts/",
			},
		},
		Compute: ComputeConfig{
			BrokerEndpoint: "http://localhost:5679",
			PrivateKey:     "", // Must be configured
			ProviderID:     "default",
			Timeout:        60 * time.Second,
			MaxRetries:     3,
			ModelConfig: ZeroGModelConfig{
				DefaultModel:     "gpt-4",
				MaxTokens:        4096,
				Temperature:      0.7,
				TopP:             0.9,
				FrequencyPenalty: 0.0,
				PresencePenalty:  0.0,
			},
			Funding: ZeroGFunding{
				AutoTopup:       false,
				MinBalance:      10.0,
				TopupAmount:     50.0,
				MaxAccountLimit: 1000.0,
			},
		},
		Scheduler: SchedulerConfig{
			Enabled:          true,
			CronExpression:   "0 6 * * 1", // Mondays at 6 AM
			BatchSize:        50,
			ConcurrencyLimit: 10,
		},
		HealthCheck: HealthCheckConfig{
			Interval: 5 * time.Minute,
			Timeout:  30 * time.Second,
		},
	}
}

package zerog

import (
	"fmt"
	"time"
)

// Config holds configuration for 0G Network adapters
type Config struct {
	Storage        StorageConfig        `yaml:"storage" json:"storage"`
	Compute        ComputeConfig        `yaml:"compute" json:"compute"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker" json:"circuit_breaker"`
}

// StorageConfig holds 0G storage configuration
type StorageConfig struct {
	RPCEndpoint      string     `yaml:"rpc_endpoint" json:"rpc_endpoint" env:"ZEROG_RPC_ENDPOINT"`
	IndexerRPC       string     `yaml:"indexer_rpc" json:"indexer_rpc" env:"ZEROG_INDEXER_RPC"`
	PrivateKey       string     `yaml:"private_key" json:"-" env:"ZEROG_PRIVATE_KEY"`
	MinReplicas      int        `yaml:"min_replicas" json:"min_replicas" env:"ZEROG_MIN_REPLICAS" envDefault:"1"`
	ExpectedReplicas int        `yaml:"expected_replicas" json:"expected_replicas" env:"ZEROG_EXPECTED_REPLICAS" envDefault:"3"`
	ChunkSize        int64      `yaml:"chunk_size" json:"chunk_size" env:"ZEROG_CHUNK_SIZE" envDefault:"10485760"`
	StorageClass     string     `yaml:"storage_class" json:"storage_class" env:"ZEROG_STORAGE_CLASS" envDefault:"standard"`
	Namespaces       Namespaces `yaml:"namespaces" json:"namespaces"`
}

// ComputeConfig holds 0G compute configuration
type ComputeConfig struct {
	BrokerEndpoint string      `yaml:"broker_endpoint" json:"broker_endpoint" env:"ZEROG_BROKER_ENDPOINT"`
	PrivateKey     string      `yaml:"private_key" json:"-" env:"ZEROG_COMPUTE_PRIVATE_KEY"`
	ProviderID     string      `yaml:"provider_id" json:"provider_id" env:"ZEROG_PROVIDER_ID"`
	Timeout        int         `yaml:"timeout" json:"timeout" env:"ZEROG_COMPUTE_TIMEOUT" envDefault:"60"`
	MaxRetries     int         `yaml:"max_retries" json:"max_retries" env:"ZEROG_MAX_RETRIES" envDefault:"3"`
	ModelConfig    ModelConfig `yaml:"model_config" json:"model_config"`
}

// ModelConfig holds AI model configuration
type ModelConfig struct {
	DefaultModel     string  `yaml:"default_model" json:"default_model" env:"ZEROG_DEFAULT_MODEL" envDefault:"gpt-3.5-turbo"`
	MaxTokens        int     `yaml:"max_tokens" json:"max_tokens" env:"ZEROG_MAX_TOKENS" envDefault:"2048"`
	Temperature      float64 `yaml:"temperature" json:"temperature" env:"ZEROG_TEMPERATURE" envDefault:"0.7"`
	TopP             float64 `yaml:"top_p" json:"top_p" env:"ZEROG_TOP_P" envDefault:"1.0"`
	FrequencyPenalty float64 `yaml:"frequency_penalty" json:"frequency_penalty" env:"ZEROG_FREQUENCY_PENALTY" envDefault:"0.0"`
	PresencePenalty  float64 `yaml:"presence_penalty" json:"presence_penalty" env:"ZEROG_PRESENCE_PENALTY" envDefault:"0.0"`
}

// CircuitBreakerConfig holds circuit breaker configuration
type CircuitBreakerConfig struct {
	MinRequests       int     `yaml:"min_requests" json:"min_requests" env:"ZEROG_CB_MIN_REQUESTS" envDefault:"10"`
	FailureThreshold  float64 `yaml:"failure_threshold" json:"failure_threshold" env:"ZEROG_CB_FAILURE_THRESHOLD" envDefault:"0.5"`
	IntervalSeconds   int     `yaml:"interval_seconds" json:"interval_seconds" env:"ZEROG_CB_INTERVAL" envDefault:"60"`
	TimeoutSeconds    int     `yaml:"timeout_seconds" json:"timeout_seconds" env:"ZEROG_CB_TIMEOUT" envDefault:"30"`
}

// Namespaces holds 0G storage namespace configuration
type Namespaces struct {
	AISummaries  string `yaml:"ai_summaries" json:"ai_summaries" env:"ZEROG_NS_AI_SUMMARIES" envDefault:"ai-summaries/"`
	AIArtifacts  string `yaml:"ai_artifacts" json:"ai_artifacts" env:"ZEROG_NS_AI_ARTIFACTS" envDefault:"ai-artifacts/"`
	ModelPrompts string `yaml:"model_prompts" json:"model_prompts" env:"ZEROG_NS_MODEL_PROMPTS" envDefault:"model-prompts/"`
}

// Validate validates the entire configuration
func (c *Config) Validate() error {
	if err := c.ValidateStorage(); err != nil {
		return fmt.Errorf("storage config validation failed: %w", err)
	}

	if err := c.ValidateCompute(); err != nil {
		return fmt.Errorf("compute config validation failed: %w", err)
	}

	return nil
}

// ValidateStorage validates storage configuration
func (c *Config) ValidateStorage() error {
	if c.Storage.RPCEndpoint == "" {
		return fmt.Errorf("storage RPC endpoint is required")
	}

	if c.Storage.IndexerRPC == "" {
		return fmt.Errorf("storage indexer RPC is required")
	}

	if c.Storage.PrivateKey == "" {
		return fmt.Errorf("storage private key is required")
	}

	if c.Storage.MinReplicas < 1 {
		return fmt.Errorf("min replicas must be at least 1")
	}

	if c.Storage.ExpectedReplicas < c.Storage.MinReplicas {
		return fmt.Errorf("expected replicas must be >= min replicas")
	}

	if c.Storage.ChunkSize < 1024 {
		return fmt.Errorf("chunk size must be at least 1024 bytes")
	}

	validStorageClasses := map[string]bool{
		"hot":      true,
		"standard": true,
		"cold":     true,
		"archival": true,
	}

	if !validStorageClasses[c.Storage.StorageClass] {
		return fmt.Errorf("invalid storage class: %s (must be hot, standard, cold, or archival)", c.Storage.StorageClass)
	}

	return nil
}

// ValidateCompute validates compute configuration
func (c *Config) ValidateCompute() error {
	if c.Compute.BrokerEndpoint == "" {
		return fmt.Errorf("compute broker endpoint is required")
	}

	if c.Compute.PrivateKey == "" {
		return fmt.Errorf("compute private key is required")
	}

	if c.Compute.Timeout < 1 {
		return fmt.Errorf("timeout must be at least 1 second")
	}

	if c.Compute.MaxRetries < 0 {
		return fmt.Errorf("max retries cannot be negative")
	}

	// Validate model config
	if c.Compute.ModelConfig.DefaultModel == "" {
		return fmt.Errorf("default model is required")
	}

	if c.Compute.ModelConfig.MaxTokens < 1 {
		return fmt.Errorf("max tokens must be at least 1")
	}

	if c.Compute.ModelConfig.Temperature < 0 || c.Compute.ModelConfig.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}

	if c.Compute.ModelConfig.TopP < 0 || c.Compute.ModelConfig.TopP > 1 {
		return fmt.Errorf("top_p must be between 0 and 1")
	}

	return nil
}

// GetComputeTimeout returns the compute timeout as a duration
func (c *Config) GetComputeTimeout() time.Duration {
	return time.Duration(c.Compute.Timeout) * time.Second
}

// GetCircuitBreakerInterval returns the circuit breaker interval as a duration
func (c *Config) GetCircuitBreakerInterval() time.Duration {
	return time.Duration(c.CircuitBreaker.IntervalSeconds) * time.Second
}

// GetCircuitBreakerTimeout returns the circuit breaker timeout as a duration
func (c *Config) GetCircuitBreakerTimeout() time.Duration {
	return time.Duration(c.CircuitBreaker.TimeoutSeconds) * time.Second
}

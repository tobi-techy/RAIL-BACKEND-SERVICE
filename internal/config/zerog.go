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
	Endpoint   string        `yaml:"endpoint" mapstructure:"endpoint"`
	Timeout    time.Duration `yaml:"timeout" mapstructure:"timeout"`
	MaxRetries int           `yaml:"max_retries" mapstructure:"max_retries"`
}

// ComputeConfig configures the 0G compute/inference client
type ComputeConfig struct {
	Endpoint   string        `yaml:"endpoint" mapstructure:"endpoint"`
	Timeout    time.Duration `yaml:"timeout" mapstructure:"timeout"`
	MaxRetries int           `yaml:"max_retries" mapstructure:"max_retries"`
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
			Endpoint:   "http://localhost:5678",
			Timeout:    30 * time.Second,
			MaxRetries: 3,
		},
		Compute: ComputeConfig{
			Endpoint:   "http://localhost:5679",
			Timeout:    60 * time.Second,
			MaxRetries: 3,
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
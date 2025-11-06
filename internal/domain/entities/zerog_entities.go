package entities

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ZeroGStorageClient interface defines operations for 0G storage
type ZeroGStorageClient interface {
	// Store uploads data to 0G storage and returns a content-addressed URI
	Store(ctx context.Context, namespace string, data []byte, metadata map[string]string) (*StorageResult, error)
	
	// Retrieve downloads data from 0G storage using a URI
	Retrieve(ctx context.Context, uri string) (*StorageData, error)
	
	// HealthCheck verifies connectivity to 0G storage network
	HealthCheck(ctx context.Context) (*HealthStatus, error)
	
	// ListObjects lists objects in a namespace (optional, for management)
	ListObjects(ctx context.Context, namespace string, prefix string) ([]StorageObject, error)
	
	// Delete removes an object from storage (if supported)
	Delete(ctx context.Context, uri string) error
}

// ZeroGInferenceGateway interface defines operations for AI inference
type ZeroGInferenceGateway interface {
	// GenerateWeeklySummary creates an AI-generated weekly portfolio summary
	GenerateWeeklySummary(ctx context.Context, request *WeeklySummaryRequest) (*InferenceResult, error)
	
	// AnalyzeOnDemand performs on-demand portfolio analysis
	AnalyzeOnDemand(ctx context.Context, request *AnalysisRequest) (*InferenceResult, error)
	
	// HealthCheck verifies connectivity to 0G compute network
	HealthCheck(ctx context.Context) (*HealthStatus, error)
	
	// GetServiceInfo returns information about available inference services
	GetServiceInfo(ctx context.Context) (*ServiceInfo, error)
}

// StorageResult represents the result of a successful storage operation
type StorageResult struct {
	URI         string            `json:"uri"`          // Content-addressed URI for retrieval
	Hash        string            `json:"hash"`         // Content hash (SHA-256)
	Size        int64             `json:"size"`         // Size in bytes
	Namespace   string            `json:"namespace"`    // Storage namespace
	Metadata    map[string]string `json:"metadata"`     // Custom metadata
	StoredAt    time.Time         `json:"stored_at"`    // Storage timestamp
	Replicas    int               `json:"replicas"`     // Number of replicas stored
	ExpiresAt   *time.Time        `json:"expires_at"`   // Optional expiration time
}

// StorageData represents retrieved data from storage
type StorageData struct {
	Data      []byte            `json:"data"`       // Retrieved data content
	URI       string            `json:"uri"`        // Original URI
	Hash      string            `json:"hash"`       // Content hash for verification
	Size      int64             `json:"size"`       // Data size
	Metadata  map[string]string `json:"metadata"`   // Associated metadata
	StoredAt  time.Time         `json:"stored_at"`  // Original storage timestamp
	ExpiresAt *time.Time        `json:"expires_at"` // Expiration time if set
}

// StorageObject represents metadata about a stored object
type StorageObject struct {
	URI       string            `json:"uri"`        // Object URI
	Hash      string            `json:"hash"`       // Content hash
	Size      int64             `json:"size"`       // Object size
	Metadata  map[string]string `json:"metadata"`   // Object metadata
	StoredAt  time.Time         `json:"stored_at"`  // Storage timestamp
	Namespace string            `json:"namespace"`  // Storage namespace
}

// WeeklySummaryRequest contains data for weekly summary generation
type WeeklySummaryRequest struct {
	UserID        uuid.UUID              `json:"user_id"`         // User identifier
	WeekStart     time.Time              `json:"week_start"`      // Start of the week
	WeekEnd       time.Time              `json:"week_end"`        // End of the week
	PortfolioData *PortfolioMetrics      `json:"portfolio_data"`  // Portfolio performance data
	Preferences   *UserPreferences       `json:"preferences"`     // User preferences for summary
	PreviousWeek  *WeeklySummaryRequest  `json:"previous_week"`   // Previous week for comparison
}

// AnalysisRequest contains data for on-demand analysis
type AnalysisRequest struct {
	UserID        uuid.UUID         `json:"user_id"`         // User identifier
	AnalysisType  string            `json:"analysis_type"`   // Type of analysis requested
	PortfolioData *PortfolioMetrics `json:"portfolio_data"`  // Current portfolio data
	Preferences   *UserPreferences  `json:"preferences"`     // User preferences
	Parameters    map[string]interface{} `json:"parameters"` // Additional parameters
}

// PortfolioMetrics contains aggregated portfolio performance data
type PortfolioMetrics struct {
	TotalValue       float64                    `json:"total_value"`        // Current total portfolio value
	TotalReturn      float64                    `json:"total_return"`       // Total return (absolute)
	TotalReturnPct   float64                    `json:"total_return_pct"`   // Total return percentage
	DayChange        float64                    `json:"day_change"`         // Daily change
	DayChangePct     float64                    `json:"day_change_pct"`     // Daily change percentage
	WeekChange       float64                    `json:"week_change"`        // Weekly change
	WeekChangePct    float64                    `json:"week_change_pct"`    // Weekly change percentage
	MonthChange      float64                    `json:"month_change"`       // Monthly change
	MonthChangePct   float64                    `json:"month_change_pct"`   // Monthly change percentage
	Positions        []PositionMetrics          `json:"positions"`          // Individual position metrics
	AllocationByBasket map[string]float64       `json:"allocation_by_basket"` // Allocation breakdown
	RiskMetrics      *RiskMetrics               `json:"risk_metrics"`       // Risk analysis
	PerformanceHistory []PerformancePoint       `json:"performance_history"` // Historical performance data
}

// PositionMetrics contains metrics for individual positions
type PositionMetrics struct {
	BasketID     uuid.UUID `json:"basket_id"`      // Basket identifier
	BasketName   string    `json:"basket_name"`    // Basket name
	Quantity     float64   `json:"quantity"`       // Position quantity
	AvgPrice     float64   `json:"avg_price"`      // Average purchase price
	CurrentValue float64   `json:"current_value"`  // Current market value
	UnrealizedPL float64   `json:"unrealized_pl"`  // Unrealized profit/loss
	UnrealizedPLPct float64 `json:"unrealized_pl_pct"` // Unrealized P&L percentage
	Weight       float64   `json:"weight"`         // Portfolio weight
}

// RiskMetrics contains portfolio risk analysis
type RiskMetrics struct {
	Volatility     float64 `json:"volatility"`      // Portfolio volatility
	Beta           float64 `json:"beta"`            // Portfolio beta
	SharpeRatio    float64 `json:"sharpe_ratio"`    // Sharpe ratio
	MaxDrawdown    float64 `json:"max_drawdown"`    // Maximum drawdown
	VaR            float64 `json:"var"`             // Value at Risk (95%)
	Diversification float64 `json:"diversification"` // Diversification score (0-1)
}

// PerformancePoint represents a point-in-time portfolio value
type PerformancePoint struct {
	Date  time.Time `json:"date"`  // Date of the data point
	Value float64   `json:"value"` // Portfolio value
	PnL   float64   `json:"pnl"`   // Profit & Loss
}

// UserPreferences contains user preferences for AI analysis
type UserPreferences struct {
	RiskTolerance   string   `json:"risk_tolerance"`    // conservative, moderate, aggressive
	PreferredStyle  string   `json:"preferred_style"`   // detailed, summary, bullet_points
	FocusAreas      []string `json:"focus_areas"`       // areas of interest (performance, risk, allocation, etc.)
	Language        string   `json:"language"`          // preferred language (default: en)
	NotificationSettings map[string]bool `json:"notification_settings"` // notification preferences
}

// InferenceResult represents the result of an AI inference operation
type InferenceResult struct {
	RequestID     string                 `json:"request_id"`      // Unique request identifier
	Content       string                 `json:"content"`         // Generated content (markdown)
	ContentType   string                 `json:"content_type"`    // Content type (text/markdown, application/json)
	Metadata      map[string]interface{} `json:"metadata"`        // Additional metadata
	TokensUsed    int                    `json:"tokens_used"`     // Number of tokens consumed
	ProcessingTime time.Duration         `json:"processing_time"` // Time taken for inference
	Model         string                 `json:"model"`           // Model used for inference
	CreatedAt     time.Time              `json:"created_at"`      // Generation timestamp
	ArtifactURI   string                 `json:"artifact_uri"`    // URI to stored detailed analysis
}

// HealthStatus represents the health status of a 0G service
type HealthStatus struct {
	Status      string                 `json:"status"`       // healthy, degraded, unhealthy
	Latency     time.Duration          `json:"latency"`      // Response latency
	Version     string                 `json:"version"`      // Service version
	Uptime      time.Duration          `json:"uptime"`       // Service uptime
	Metrics     map[string]interface{} `json:"metrics"`      // Service-specific metrics
	LastChecked time.Time              `json:"last_checked"` // Last health check time
	Errors      []string               `json:"errors"`       // Any error messages
}

// ServiceInfo contains information about available inference services
type ServiceInfo struct {
	ProviderID   string                 `json:"provider_id"`    // Provider identifier
	ServiceName  string                 `json:"service_name"`   // Service name
	Models       []ModelInfo            `json:"models"`         // Available models
	Pricing      *PricingInfo           `json:"pricing"`        // Pricing information
	Capabilities []string               `json:"capabilities"`   // Service capabilities
	Status       string                 `json:"status"`         // Service status
	Metadata     map[string]interface{} `json:"metadata"`       // Additional service metadata
}

// ModelInfo contains information about an AI model
type ModelInfo struct {
	ModelID      string    `json:"model_id"`       // Model identifier
	Name         string    `json:"name"`           // Human-readable model name
	Description  string    `json:"description"`    // Model description
	MaxTokens    int       `json:"max_tokens"`     // Maximum tokens per request
	InputCost    float64   `json:"input_cost"`     // Cost per input token
	OutputCost   float64   `json:"output_cost"`    // Cost per output token
	Version      string    `json:"version"`        // Model version
	UpdatedAt    time.Time `json:"updated_at"`     // Last update timestamp
}

// PricingInfo contains service pricing information
type PricingInfo struct {
	Currency      string  `json:"currency"`       // Pricing currency
	BaseRate      float64 `json:"base_rate"`      // Base rate
	TokenRate     float64 `json:"token_rate"`     // Rate per token
	MinimumCharge float64 `json:"minimum_charge"` // Minimum charge per request
}

// ZeroGError represents errors from 0G operations
type ZeroGError struct {
	Code      string                 `json:"code"`      // Error code
	Message   string                 `json:"message"`   // Error message
	Details   map[string]interface{} `json:"details"`   // Additional error details
	Retryable bool                   `json:"retryable"` // Whether the operation can be retried
	Timestamp time.Time              `json:"timestamp"` // Error timestamp
}

func (e *ZeroGError) Error() string {
	return e.Message
}

// Operation result types for internal use
type OperationResult struct {
	Success   bool                   `json:"success"`
	Data      interface{}            `json:"data,omitempty"`
	Error     *ZeroGError            `json:"error,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Duration  time.Duration          `json:"duration"`
	Timestamp time.Time              `json:"timestamp"`
}

// Namespace constants for storage organization
const (
	NamespaceAISummaries  = "ai-summaries/"
	NamespaceAIArtifacts  = "ai-artifacts/"
	NamespaceModelPrompts = "model-prompts/"
)

// Analysis types for on-demand analysis
const (
	AnalysisTypeDiversification = "diversification"
	AnalysisTypeRisk           = "risk"
	AnalysisTypePerformance    = "performance"
	AnalysisTypeAllocation     = "allocation"
	AnalysisTypeRebalancing    = "rebalancing"
)

// Health status constants
const (
	HealthStatusHealthy   = "healthy"
	HealthStatusDegraded  = "degraded"
	HealthStatusUnhealthy = "unhealthy"
)

// Error codes for 0G operations
const (
	ErrorCodeNetworkError    = "NETWORK_ERROR"
	ErrorCodeAuthError       = "AUTH_ERROR"
	ErrorCodeInvalidRequest  = "INVALID_REQUEST"
	ErrorCodeQuotaExceeded   = "QUOTA_EXCEEDED"
	ErrorCodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	ErrorCodeInternalError   = "INTERNAL_ERROR"
	ErrorCodeTimeout         = "TIMEOUT"
	ErrorCodeInvalidURI      = "INVALID_URI"
	ErrorCodeNotFound        = "NOT_FOUND"
	ErrorCodeInsufficientFunds = "INSUFFICIENT_FUNDS"
)
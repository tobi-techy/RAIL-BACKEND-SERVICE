package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTP metrics
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stack_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status_code"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stack_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	// Business metrics
	TransactionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stack_transactions_total",
			Help: "Total number of transactions processed",
		},
		[]string{"type", "status", "currency"},
	)

	TransactionAmount = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stack_transaction_amount_usd",
			Help:    "Transaction amounts in USD",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000, 50000},
		},
		[]string{"type", "currency"},
	)

	UserBalanceGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stack_user_balance_usd",
			Help: "User balance in USD",
		},
		[]string{"user_id", "currency"},
	)

	ActiveUsersGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "stack_active_users_total",
			Help: "Total number of active users",
		},
	)

	// System metrics
	DatabaseConnectionsGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stack_database_connections",
			Help: "Number of database connections",
		},
		[]string{"state"}, // open, idle, in_use
	)

	DatabaseQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stack_database_query_duration_seconds",
			Help:    "Database query duration in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0},
		},
		[]string{"operation", "table"},
	)

	RedisOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stack_redis_operation_duration_seconds",
			Help:    "Redis operation duration in seconds",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1},
		},
		[]string{"operation"},
	)

	// External service metrics
	ExternalAPICallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stack_external_api_calls_total",
			Help: "Total number of external API calls",
		},
		[]string{"service", "endpoint", "status_code"},
	)

	ExternalAPICallDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "stack_external_api_call_duration_seconds",
			Help:    "External API call duration in seconds",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
		},
		[]string{"service", "endpoint"},
	)

	CircuitBreakerStateGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "stack_circuit_breaker_state",
			Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
		},
		[]string{"service"},
	)

	// Security metrics
	AuthenticationAttemptsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stack_authentication_attempts_total",
			Help: "Total number of authentication attempts",
		},
		[]string{"result"}, // success, failed, blocked
	)

	RateLimitHitsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stack_rate_limit_hits_total",
			Help: "Total number of rate limit hits",
		},
		[]string{"tier", "endpoint"},
	)

	// Audit metrics
	AuditEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stack_audit_events_total",
			Help: "Total number of audit events",
		},
		[]string{"action", "resource", "status"},
	)
)

// RecordHTTPRequest records HTTP request metrics
func RecordHTTPRequest(method, endpoint, statusCode string, duration float64) {
	HTTPRequestsTotal.WithLabelValues(method, endpoint, statusCode).Inc()
	HTTPRequestDuration.WithLabelValues(method, endpoint).Observe(duration)
}

// RecordTransaction records transaction metrics
func RecordTransaction(txType, status, currency string, amount float64) {
	TransactionsTotal.WithLabelValues(txType, status, currency).Inc()
	if amount > 0 {
		TransactionAmount.WithLabelValues(txType, currency).Observe(amount)
	}
}

// UpdateUserBalance updates user balance gauge
func UpdateUserBalance(userID, currency string, balance float64) {
	UserBalanceGauge.WithLabelValues(userID, currency).Set(balance)
}

// RecordDatabaseQuery records database query metrics
func RecordDatabaseQuery(operation, table string, duration float64) {
	DatabaseQueryDuration.WithLabelValues(operation, table).Observe(duration)
}

// RecordRedisOperation records Redis operation metrics
func RecordRedisOperation(operation string, duration float64) {
	RedisOperationDuration.WithLabelValues(operation).Observe(duration)
}

// RecordExternalAPICall records external API call metrics
func RecordExternalAPICall(service, endpoint, statusCode string, duration float64) {
	ExternalAPICallsTotal.WithLabelValues(service, endpoint, statusCode).Inc()
	ExternalAPICallDuration.WithLabelValues(service, endpoint).Observe(duration)
}

// UpdateCircuitBreakerState updates circuit breaker state
func UpdateCircuitBreakerState(service string, state float64) {
	CircuitBreakerStateGauge.WithLabelValues(service).Set(state)
}

// RecordAuthenticationAttempt records authentication attempt
func RecordAuthenticationAttempt(result string) {
	AuthenticationAttemptsTotal.WithLabelValues(result).Inc()
}

// RecordRateLimitHit records rate limit hit
func RecordRateLimitHit(tier, endpoint string) {
	RateLimitHitsTotal.WithLabelValues(tier, endpoint).Inc()
}

// RecordAuditEvent records audit event
func RecordAuditEvent(action, resource, status string) {
	AuditEventsTotal.WithLabelValues(action, resource, status).Inc()
}

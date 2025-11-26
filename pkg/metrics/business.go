package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// BusinessMetrics contains all business-specific metrics
type BusinessMetrics struct {
	// Order metrics
	OrdersCreated     *prometheus.CounterVec
	OrdersFilled      *prometheus.CounterVec
	OrdersFailed      *prometheus.CounterVec
	OrderValue        *prometheus.HistogramVec
	OrderDuration     *prometheus.HistogramVec
	
	// Deposit metrics
	DepositsInitiated *prometheus.CounterVec
	DepositsCompleted *prometheus.CounterVec
	DepositsFailed    *prometheus.CounterVec
	DepositAmount     *prometheus.HistogramVec
	DepositDuration   *prometheus.HistogramVec
	
	// Withdrawal metrics
	WithdrawalsInitiated *prometheus.CounterVec
	WithdrawalsCompleted *prometheus.CounterVec
	WithdrawalsFailed    *prometheus.CounterVec
	WithdrawalAmount     *prometheus.HistogramVec
	WithdrawalDuration   *prometheus.HistogramVec
	
	// User metrics
	UsersRegistered prometheus.Counter
	UsersKYCApproved prometheus.Counter
	UsersKYCRejected prometheus.Counter
	ActiveUsers     prometheus.Gauge
	
	// Balance metrics
	TotalBalance    prometheus.Gauge
	AverageBalance  prometheus.Gauge
	
	// External API metrics
	AlpacaAPILatency  *prometheus.HistogramVec
	AlpacaAPIErrors   *prometheus.CounterVec
	CircleAPILatency  *prometheus.HistogramVec
	CircleAPIErrors   *prometheus.CounterVec
	
	// Basket metrics
	BasketOrdersCreated *prometheus.CounterVec
	BasketCompositions  *prometheus.GaugeVec
}

// NewBusinessMetrics creates and registers all business metrics
func NewBusinessMetrics() *BusinessMetrics {
	return &BusinessMetrics{
		// Order metrics
		OrdersCreated: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_orders_created_total",
				Help: "Total number of orders created",
			},
			[]string{"asset_type", "side"},
		),
		OrdersFilled: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_orders_filled_total",
				Help: "Total number of orders filled",
			},
			[]string{"asset_type", "side"},
		),
		OrdersFailed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_orders_failed_total",
				Help: "Total number of orders failed",
			},
			[]string{"asset_type", "side", "reason"},
		),
		OrderValue: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "stack_order_value_usd",
				Help:    "Distribution of order values in USD",
				Buckets: []float64{10, 50, 100, 500, 1000, 5000, 10000, 50000},
			},
			[]string{"asset_type"},
		),
		OrderDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "stack_order_duration_seconds",
				Help:    "Time from order creation to fill",
				Buckets: []float64{1, 5, 10, 30, 60, 300, 600, 1800},
			},
			[]string{"asset_type"},
		),
		
		// Deposit metrics
		DepositsInitiated: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_deposits_initiated_total",
				Help: "Total number of deposits initiated",
			},
			[]string{"chain"},
		),
		DepositsCompleted: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_deposits_completed_total",
				Help: "Total number of deposits completed",
			},
			[]string{"chain"},
		),
		DepositsFailed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_deposits_failed_total",
				Help: "Total number of deposits failed",
			},
			[]string{"chain", "reason"},
		),
		DepositAmount: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "stack_deposit_amount_usd",
				Help:    "Distribution of deposit amounts in USD",
				Buckets: []float64{10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000},
			},
			[]string{"chain"},
		),
		DepositDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "stack_deposit_duration_seconds",
				Help:    "Time from deposit initiation to completion",
				Buckets: []float64{30, 60, 300, 600, 1800, 3600, 7200},
			},
			[]string{"chain"},
		),
		
		// Withdrawal metrics
		WithdrawalsInitiated: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_withdrawals_initiated_total",
				Help: "Total number of withdrawals initiated",
			},
			[]string{"chain"},
		),
		WithdrawalsCompleted: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_withdrawals_completed_total",
				Help: "Total number of withdrawals completed",
			},
			[]string{"chain"},
		),
		WithdrawalsFailed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_withdrawals_failed_total",
				Help: "Total number of withdrawals failed",
			},
			[]string{"chain", "reason"},
		),
		WithdrawalAmount: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "stack_withdrawal_amount_usd",
				Help:    "Distribution of withdrawal amounts in USD",
				Buckets: []float64{10, 50, 100, 500, 1000, 5000, 10000, 50000, 100000},
			},
			[]string{"chain"},
		),
		WithdrawalDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "stack_withdrawal_duration_seconds",
				Help:    "Time from withdrawal initiation to completion",
				Buckets: []float64{30, 60, 300, 600, 1800, 3600, 7200},
			},
			[]string{"chain"},
		),
		
		// User metrics
		UsersRegistered: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "stack_users_registered_total",
				Help: "Total number of users registered",
			},
		),
		UsersKYCApproved: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "stack_users_kyc_approved_total",
				Help: "Total number of users with approved KYC",
			},
		),
		UsersKYCRejected: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "stack_users_kyc_rejected_total",
				Help: "Total number of users with rejected KYC",
			},
		),
		ActiveUsers: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "stack_active_users",
				Help: "Number of active users",
			},
		),
		
		// Balance metrics
		TotalBalance: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "stack_total_balance_usd",
				Help: "Total balance across all users in USD",
			},
		),
		AverageBalance: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "stack_average_balance_usd",
				Help: "Average balance per user in USD",
			},
		),
		
		// External API metrics
		AlpacaAPILatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "stack_alpaca_api_latency_seconds",
				Help:    "Alpaca API request latency",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint", "method"},
		),
		AlpacaAPIErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_alpaca_api_errors_total",
				Help: "Total number of Alpaca API errors",
			},
			[]string{"endpoint", "method", "status_code"},
		),
		CircleAPILatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "stack_circle_api_latency_seconds",
				Help:    "Circle API request latency",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint", "method"},
		),
		CircleAPIErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_circle_api_errors_total",
				Help: "Total number of Circle API errors",
			},
			[]string{"endpoint", "method", "status_code"},
		),
		
		// Basket metrics
		BasketOrdersCreated: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "stack_basket_orders_created_total",
				Help: "Total number of basket orders created",
			},
			[]string{"basket_id"},
		),
		BasketCompositions: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "stack_basket_composition_percent",
				Help: "Current composition percentage of baskets",
			},
			[]string{"basket_id", "symbol"},
		),
	}
}

// Global business metrics instance
var Business *BusinessMetrics

// InitBusinessMetrics initializes the global business metrics
func InitBusinessMetrics() {
	Business = NewBusinessMetrics()
}

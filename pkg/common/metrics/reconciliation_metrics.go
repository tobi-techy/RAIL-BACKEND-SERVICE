package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Global reconciliation metrics exported for use in DI container
var (
	ReconciliationRunsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "runs_total",
			Help:      "Total number of reconciliation runs",
		},
		[]string{"run_type", "status"},
	)
	
	ReconciliationRunsInProgress = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "runs_in_progress",
			Help:      "Number of reconciliation runs currently in progress",
		},
		[]string{"run_type"},
	)
	
	ReconciliationChecksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "checks_total",
			Help:      "Total number of reconciliation checks executed",
		},
		[]string{"check_type"},
	)
	
	ReconciliationCheckDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "check_duration_seconds",
			Help:      "Duration of individual reconciliation checks in seconds",
			Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30},
		},
		[]string{"check_type"},
	)
	
	ReconciliationChecksPassed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "checks_passed_total",
			Help:      "Total number of reconciliation checks that passed",
		},
		[]string{"check_type"},
	)
	
	ReconciliationChecksFailed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "checks_failed_total",
			Help:      "Total number of reconciliation checks that failed",
		},
		[]string{"check_type"},
	)
	
	ReconciliationExceptionsAutoCorrected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "exceptions_auto_corrected_total",
			Help:      "Total number of exceptions automatically corrected",
		},
		[]string{"check_type"},
	)
	
	ReconciliationDiscrepancyAmount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "discrepancy_amount",
			Help:      "Amount of discrepancy detected in reconciliation checks",
		},
		[]string{"check_type", "currency"},
	)
	
	ReconciliationAlertsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "stack",
			Subsystem: "reconciliation",
			Name:      "alerts_total",
			Help:      "Total number of reconciliation alerts sent",
		},
		[]string{"check_type", "severity"},
	)
)

// ReconciliationMetrics holds Prometheus metrics for reconciliation
type ReconciliationMetrics struct {
	// Run metrics
	runsTotal *prometheus.CounterVec
	runDuration *prometheus.HistogramVec
	runsInProgress *prometheus.GaugeVec
	
	// Check metrics
	checksTotal *prometheus.CounterVec
	checkDuration *prometheus.HistogramVec
	checksPassed *prometheus.CounterVec
	checksFailed *prometheus.CounterVec
	
	// Exception metrics
	exceptionsTotal *prometheus.CounterVec
	exceptionsUnresolved *prometheus.GaugeVec
	exceptionsBySeverity *prometheus.GaugeVec
	exceptionsAutoCorrected *prometheus.CounterVec
	
	// Discrepancy metrics
	discrepancyAmount *prometheus.GaugeVec
	
	// Alert metrics
	alertsTotal *prometheus.CounterVec
}

// NewReconciliationMetrics creates and registers reconciliation metrics
func NewReconciliationMetrics(namespace string) *ReconciliationMetrics {
	return &ReconciliationMetrics{
		runsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "runs_total",
				Help:      "Total number of reconciliation runs",
			},
			[]string{"run_type", "status"},
		),
		runDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "run_duration_seconds",
				Help:      "Duration of reconciliation runs in seconds",
				Buckets:   []float64{1, 5, 10, 30, 60, 120, 300, 600},
			},
			[]string{"run_type"},
		),
		runsInProgress: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "runs_in_progress",
				Help:      "Number of reconciliation runs currently in progress",
			},
			[]string{"run_type"},
		),
		checksTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "checks_total",
				Help:      "Total number of reconciliation checks executed",
			},
			[]string{"check_type"},
		),
		checkDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "check_duration_seconds",
				Help:      "Duration of individual reconciliation checks in seconds",
				Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30},
			},
			[]string{"check_type"},
		),
		checksPassed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "checks_passed_total",
				Help:      "Total number of reconciliation checks that passed",
			},
			[]string{"check_type"},
		),
		checksFailed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "checks_failed_total",
				Help:      "Total number of reconciliation checks that failed",
			},
			[]string{"check_type"},
		),
		exceptionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "exceptions_total",
				Help:      "Total number of reconciliation exceptions detected",
			},
			[]string{"check_type", "severity"},
		),
		exceptionsUnresolved: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "exceptions_unresolved",
				Help:      "Number of unresolved reconciliation exceptions",
			},
			[]string{"severity"},
		),
		exceptionsBySeverity: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "exceptions_by_severity",
				Help:      "Number of exceptions grouped by severity",
			},
			[]string{"severity"},
		),
		exceptionsAutoCorrected: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "exceptions_auto_corrected_total",
				Help:      "Total number of exceptions automatically corrected",
			},
			[]string{"check_type"},
		),
		discrepancyAmount: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "discrepancy_amount",
				Help:      "Amount of discrepancy detected in reconciliation checks",
			},
			[]string{"check_type", "currency"},
		),
		alertsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "reconciliation",
				Name:      "alerts_total",
				Help:      "Total number of reconciliation alerts sent",
			},
			[]string{"check_type", "severity"},
		),
	}
}


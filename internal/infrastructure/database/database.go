package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/sony/gobreaker"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
)

var circuitBreaker *gobreaker.CircuitBreaker

func init() {
	settings := gobreaker.Settings{
		Name:        "database",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
	}
	circuitBreaker = gobreaker.NewCircuitBreaker(settings)
}

// NewConnection creates a new database connection with enhanced configuration
func NewConnection(cfg config.DatabaseConfig) (*sql.DB, error) {
	var db *sql.DB
	var err error

	_, cbErr := circuitBreaker.Execute(func() (interface{}, error) {
		db, err = sql.Open("postgres", cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to open database connection: %w", err)
		}

		// Enhanced connection pool settings
		maxOpen := cfg.MaxOpenConns
		if maxOpen == 0 {
			maxOpen = 25
		}
		maxIdle := cfg.MaxIdleConns
		if maxIdle == 0 {
			maxIdle = 5
		}
		connLifetime := cfg.ConnMaxLifetime
		if connLifetime == 0 {
			connLifetime = 300
		}
		db.SetMaxOpenConns(maxOpen)
		db.SetMaxIdleConns(maxIdle)
		db.SetConnMaxLifetime(time.Duration(connLifetime) * time.Second)
		db.SetConnMaxIdleTime(5 * time.Minute)

		// Test connection with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err = db.PingContext(ctx); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to ping database: %w", err)
		}

		return db, nil
	})

	if cbErr != nil {
		return nil, fmt.Errorf("circuit breaker: %w", cbErr)
	}

	return db, err
}

// RunMigrations runs database migrations
func RunMigrations(databaseURL string) error {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("failed to open database for migrations: %w", err)
	}
	defer db.Close()

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create postgres driver: %w", err)
	}

	migrationPath := filepath.ToSlash(filepath.Clean("migrations"))
	m, err := migrate.NewWithDatabaseInstance(
		"file://"+migrationPath,
		"postgres",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

// HealthCheck checks if database is accessible
func HealthCheck(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("database health check failed: %w", err)
	}

	return nil
}

// WithTransaction executes a function within a database transaction
func WithTransaction(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		} else if err != nil {
			tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(tx)
	return err
}

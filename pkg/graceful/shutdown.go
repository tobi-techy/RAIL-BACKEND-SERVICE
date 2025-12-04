package graceful

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rail-service/rail_service/pkg/logger"
)

type Shutdowner interface {
	Shutdown(timeout time.Duration) error
}

type ShutdownManager struct {
	server      *http.Server
	db          *sql.DB
	shutdowners []Shutdowner
	logger      *logger.Logger
}

func NewShutdownManager(server *http.Server, db *sql.DB, logger *logger.Logger) *ShutdownManager {
	return &ShutdownManager{
		server:      server,
		db:          db,
		shutdowners: make([]Shutdowner, 0),
		logger:      logger,
	}
}

func (sm *ShutdownManager) Register(s Shutdowner) {
	sm.shutdowners = append(sm.shutdowners, s)
}

func (sm *ShutdownManager) WaitForShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	sm.logger.Info("Shutting down gracefully...")

	timeout := 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Shutdown registered components
	for _, s := range sm.shutdowners {
		if err := s.Shutdown(timeout); err != nil {
			sm.logger.Warn("Component shutdown error", "error", err)
		}
	}

	// Shutdown HTTP server
	if err := sm.server.Shutdown(ctx); err != nil {
		sm.logger.Error("Server forced shutdown", "error", err)
	}

	// Close database
	if err := sm.db.Close(); err != nil {
		sm.logger.Warn("Database close error", "error", err)
	}

	sm.logger.Info("Shutdown complete")
}

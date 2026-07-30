package simulation

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	aiservice "github.com/rail-service/rail_service/internal/domain/services/ai"
	"github.com/rail-service/rail_service/internal/infrastructure/config"
	"github.com/rail-service/rail_service/internal/infrastructure/database"
	"github.com/rail-service/rail_service/internal/infrastructure/di"
	"github.com/rail-service/rail_service/pkg/logger"
	"go.uber.org/zap"
)

// MiriamChat is the single orchestrator method the simulation drives. Depending on
// this narrow surface (rather than the full aiservice.ChatEngine) keeps the harness
// decoupled and makes an offline stub trivial. The production aiservice.ChatEngine
// (backed by core.Agent) satisfies it.
type MiriamChat interface {
	ChatWithConversationWithOptions(ctx context.Context, userID uuid.UUID, conv *entities.AIConversation, message string, opts aiservice.ChatOptions) (*aiservice.ChatResponse, error)
}

// Harness owns the booted application container the simulation drives. It boots the
// SAME dependency graph the production app uses (di.NewContainer) so Miriam behaves
// exactly as she does in production — only the database and AI keys differ.
type Harness struct {
	cfg       *config.Config
	log       *logger.Logger
	db        *sql.DB
	container *di.Container

	seeder  *Seeder
	anomaly *aiservice.AnomalyEngine

	// chat is the orchestrator under test. Defaults to the real container
	// orchestrator; SetChat can override it with a stub for offline runs.
	chat MiriamChat
}

// HarnessConfig controls how the harness boots.
type HarnessConfig struct {
	// DatabaseURL overrides the config/env database URL. REQUIRED to be a
	// non-production database; the harness refuses to run against a URL that looks
	// production-ish unless AllowUnsafeDB is set.
	DatabaseURL   string
	AllowUnsafeDB bool
	// Quiet forces the internal logger to ERROR only, suppressing INFO/DEBUG/WARN
	// noise so live simulation output stays clean.
	Quiet bool
}

// NewHarness loads config, connects to the simulation database, runs migrations, and
// builds the DI container. The AI provider keys come from the normal config/env so
// Miriam, the persona, and the judge all use real models unless the caller injects a
// stub ChatEngine.
func NewHarness(hc HarnessConfig) (*Harness, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	dbURL := hc.DatabaseURL
	if dbURL == "" {
		dbURL = os.Getenv("SIM_DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = cfg.Database.URL
	}
	if dbURL == "" {
		return nil, fmt.Errorf("no database URL: set SIM_DATABASE_URL or --db")
	}
	if err := guardNonProdDB(dbURL, hc.AllowUnsafeDB); err != nil {
		return nil, err
	}
	cfg.Database.URL = dbURL
	// Force the eval-style wiring on; the harness talks to the orchestrator directly,
	// but this keeps behavior consistent with the gated eval path.
	cfg.Eval.Enabled = true
	if cfg.Eval.Token == "" {
		cfg.Eval.Token = "sim-harness"
	}
	// Turn on Miriam's deterministic response guard for the simulation so runs
	// measure the enforced (production-target) behavior, not the raw model output.
	cfg.AI.ResponseGuard = true
	// Suppress noisy INFO/DEBUG/WARN logs in quiet (live) mode.
	if hc.Quiet {
		cfg.LogLevel = "error"
	}

	log := logger.New(cfg.LogLevel, cfg.Environment)

	db, err := database.NewConnection(cfg.Database, cfg.Environment)
	if err != nil {
		return nil, fmt.Errorf("connect sim database: %w", err)
	}

	if err := database.RunMigrations(cfg.Database.URL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations on sim database: %w", err)
	}

	container, err := di.NewContainer(cfg, db, log)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("build container: %w", err)
	}

	h := &Harness{cfg: cfg, log: log, db: db, container: container, chat: container.AIOrchestrator}

	if container.LedgerService == nil || container.MiriamIntelligenceService == nil || container.UserRepo == nil {
		return nil, fmt.Errorf("container is missing services required for seeding (ledger/miriam/user)")
	}
	h.seeder = NewSeeder(db, container.LedgerService, container.MiriamIntelligenceService, container.UserRepo)

	// Build the real anomaly engine over the ledger spending repo — the same wiring
	// application.go uses — so proactivity scenarios exercise production detection.
	if container.LedgerSpendingRepo != nil {
		h.anomaly = aiservice.NewAnomalyEngine(
			container.LedgerSpendingRepo,
			container.LedgerSpendingRepo,
			container.LedgerSpendingRepo,
			container.LedgerSpendingRepo,
			log.Zap(),
		)
	}

	return h, nil
}

// Orchestrator returns the Miriam chat engine the harness will drive.
func (h *Harness) Orchestrator() MiriamChat { return h.chat }

// SetChat overrides the orchestrator under test (used by --stub-miriam for offline
// self-tests). Passing nil is a no-op.
func (h *Harness) SetChat(c MiriamChat) {
	if c != nil {
		h.chat = c
	}
}

// Zap returns the harness logger.
func (h *Harness) Zap() *zap.Logger { return h.log.Zap() }

// DB returns the underlying simulation database handle (used by the orphan sweeper).
func (h *Harness) DB() *sql.DB { return h.db }

// backgroundWriteDrainer is implemented by orchestrators that run fire-and-forget
// writes (memory extraction) after a turn. The harness drains them before
// teardown so async INSERTs don't race the user delete.
type backgroundWriteDrainer interface {
	WaitForBackgroundWrites(ctx context.Context) error
}

// DrainBackgroundWrites blocks until the orchestrator's async post-turn writes
// settle, or the context deadline hits. No-op if the orchestrator doesn't
// support draining (e.g. the offline stub).
func (h *Harness) DrainBackgroundWrites(ctx context.Context) {
	if d, ok := h.chat.(backgroundWriteDrainer); ok {
		if err := d.WaitForBackgroundWrites(ctx); err != nil {
			h.log.Zap().Warn("drain background writes: " + err.Error())
		}
	}
}

// ModelName returns a best-effort identifier of the model powering Miriam, for
// stamping persisted run records. Falls back to the provider manager name.
func (h *Harness) ModelName() string {
	if h.container != nil && h.container.AIProvider != nil {
		return h.container.AIProvider.Name()
	}
	return "unknown"
}

// Close releases the database connection.
func (h *Harness) Close() error {
	if h.db != nil {
		return h.db.Close()
	}
	return nil
}

// guardNonProdDB refuses to run against a database URL that looks like production,
// because the harness creates synthetic users and writes ledger rows.
func guardNonProdDB(url string, allow bool) error {
	if allow {
		return nil
	}
	lower := strings.ToLower(url)
	for _, marker := range []string{"prod", "production", "amazonaws.com", "rds.", "supabase", "neon.tech"} {
		if strings.Contains(lower, marker) {
			return fmt.Errorf("refusing to run simulation against a production-looking database (%q). "+
				"Use a disposable DB, or pass AllowUnsafeDB if you are certain", marker)
		}
	}
	return nil
}

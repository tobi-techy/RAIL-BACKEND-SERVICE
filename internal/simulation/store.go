package simulation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RunRecord is one persisted scenario outcome, stamped with enough provenance to build
// trend/regression views across a multi-day soak (git SHA, model, scenario hash).
type RunRecord struct {
	RunID        string    `json:"run_id"`
	SoakID       string    `json:"soak_id"`
	ScenarioID   string    `json:"scenario_id"`
	ScenarioHash string    `json:"scenario_hash"`
	Archetype    string    `json:"archetype,omitempty"`
	GitSHA       string    `json:"git_sha,omitempty"`
	Model        string    `json:"model,omitempty"`
	Impact       float64   `json:"impact"`
	SafetyPass   bool      `json:"safety_pass"`
	DurationMs   int64     `json:"duration_ms"`
	MiriamTokens int       `json:"miriam_tokens"`
	Turns        int       `json:"turns"`
	Error        string    `json:"error,omitempty"`
	Card         Scorecard `json:"card"`
	CreatedAt    time.Time `json:"created_at"`
}

// Store persists run records. JSONL is the durable source of truth (append-only,
// survives DB resets and crashes); the Postgres mirror is optional and only used for
// convenient trend queries.
type Store struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder

	db      *sql.DB // optional
	pgReady bool
}

// StoreConfig configures persistence.
type StoreConfig struct {
	JSONLPath string  // required: append-only results log
	DB        *sql.DB // optional: also mirror into simulation_runs
}

// NewStore opens (creating if needed) the JSONL sink and, if a DB is supplied, ensures
// the mirror table exists.
func NewStore(ctx context.Context, cfg StoreConfig) (*Store, error) {
	if cfg.JSONLPath == "" {
		return nil, fmt.Errorf("store: JSONLPath is required")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.JSONLPath), 0o755); err != nil {
		return nil, fmt.Errorf("store: mkdir: %w", err)
	}
	f, err := os.OpenFile(cfg.JSONLPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("store: open jsonl: %w", err)
	}
	s := &Store{file: f, enc: json.NewEncoder(f), db: cfg.DB}
	if cfg.DB != nil {
		if err := s.ensureTable(ctx); err != nil {
			// A missing mirror must not kill a soak — JSONL still captures everything.
			_ = f.Close()
			return nil, fmt.Errorf("store: ensure table: %w", err)
		}
		s.pgReady = true
	}
	return s, nil
}

func (s *Store) ensureTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS simulation_runs (
    run_id        UUID PRIMARY KEY,
    soak_id       TEXT NOT NULL,
    scenario_id   TEXT NOT NULL,
    scenario_hash TEXT NOT NULL,
    archetype     TEXT,
    git_sha       TEXT,
    model         TEXT,
    impact        DOUBLE PRECISION NOT NULL,
    safety_pass   BOOLEAN NOT NULL,
    duration_ms   BIGINT NOT NULL,
    miriam_tokens INTEGER NOT NULL,
    turns         INTEGER NOT NULL,
    error         TEXT,
    card          JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_simulation_runs_soak ON simulation_runs(soak_id);
CREATE INDEX IF NOT EXISTS idx_simulation_runs_scenario ON simulation_runs(scenario_id);
CREATE INDEX IF NOT EXISTS idx_simulation_runs_created ON simulation_runs(created_at DESC);`)
	return err
}

// Append persists one record: JSONL first (durability), then the optional DB mirror. A
// DB failure is non-fatal because JSONL already holds the source of truth.
func (s *Store) Append(ctx context.Context, rec RunRecord) error {
	if rec.RunID == "" {
		rec.RunID = uuid.NewString()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.enc.Encode(&rec); err != nil {
		return fmt.Errorf("store: write jsonl: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("store: fsync jsonl: %w", err)
	}

	if s.pgReady {
		if err := s.insertRow(ctx, rec); err != nil {
			// Log-and-continue semantics: return nil so the soak keeps running, but
			// surface via a wrapped sentinel the caller may choose to log.
			return fmt.Errorf("%w: %v", errMirror, err)
		}
	}
	return nil
}

var errMirror = fmt.Errorf("store: db mirror failed (jsonl still written)")

// IsMirrorError reports whether err is a non-fatal DB-mirror failure.
func IsMirrorError(err error) bool {
	return err != nil && (err == errMirror || (err != nil && wrapsMirror(err)))
}

func wrapsMirror(err error) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if err == errMirror {
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (s *Store) insertRow(ctx context.Context, rec RunRecord) error {
	cardJSON, err := json.Marshal(rec.Card)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO simulation_runs
  (run_id, soak_id, scenario_id, scenario_hash, archetype, git_sha, model, impact, safety_pass, duration_ms, miriam_tokens, turns, error, card, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (run_id) DO NOTHING`,
		rec.RunID, rec.SoakID, rec.ScenarioID, rec.ScenarioHash, nullStr(rec.Archetype),
		nullStr(rec.GitSHA), nullStr(rec.Model), rec.Impact, rec.SafetyPass, rec.DurationMs,
		rec.MiriamTokens, rec.Turns, nullStr(rec.Error), cardJSON, rec.CreatedAt,
	)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Close flushes and closes the JSONL file. The DB (owned by the caller) is left open.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

package app

import (
	"context"
	"errors"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/pkg/alerting"
)

const (
	workerLeaderTTL   = 30 * time.Second
	workerLeaderRenew = 10 * time.Second
	workerLeaderRetry = 5 * time.Second
)

// shouldStandDownAfterRenewFailure decides whether a failed lease renewal must
// stop the worker fleet. A single transient failure (Redis pool timeout,
// network blip) must NOT tear down ~30 workers — the teardown/restart storm
// then re-saturates Redis and Postgres and the system flap-loops (observed in
// production 2026-08-24: five full fleet restarts in 3.5 minutes). We stand
// down only when leadership has DEFINITIVELY lapsed: another owner holds the
// lease (ErrNotLeader), or failures have spanned a full TTL since the last
// successful renewal — past that point we cannot be sure we are still leader,
// so stopping is the safe move.
func shouldStandDownAfterRenewFailure(err error, lastRenewed, now time.Time) bool {
	if errors.Is(err, cache.ErrNotLeader) {
		return true
	}
	return now.Sub(lastRenewed) >= workerLeaderTTL
}

func (app *Application) startRedisMonitor() {
	if app.container == nil || app.container.RedisClient == nil {
		return
	}
	// Leadership flap cycles call this repeatedly (every stopJobWorkers);
	// stop any previous monitor first so they don't stack.
	app.stopRedisMonitor()
	alerter := alerting.NewTelegramAlerter(app.cfg.TelegramAlerts.BotToken, app.cfg.TelegramAlerts.ChatID)
	monitor := cache.NewHealthMonitor(app.container.RedisClient, app.log.Zap(), 60*time.Second, func(up bool, err error) {
		if up {
			if alerter != nil {
				alerter.SendFatal("✅ Redis recovered", nil)
			}
			return
		}
		app.log.Error("Redis is DOWN — auth blacklist failing closed, rate limiting degraded", "error", err)
		if alerter != nil {
			alerter.SendFatal("🚨 Redis DOWN (check Upstash budget/suspension)", err)
		}
	})
	monCtx, monCancel := context.WithCancel(context.Background())
	app.monitorCancel = monCancel
	app.redisMonitor = monitor
	monitor.Start(monCtx)
}

func (app *Application) stopRedisMonitor() {
	if app.monitorCancel != nil {
		app.monitorCancel()
		app.monitorCancel = nil
	}
	if app.redisMonitor != nil {
		app.redisMonitor.Wait()
		app.redisMonitor = nil
	}
}

// workerStartDecision is what a replica should do with the background worker
// fleet after the leader lease has been probed.
type workerStartDecision int

const (
	// startWorkerFleet: this process runs the ~30 background workers.
	startWorkerFleet workerStartDecision = iota
	// stayHTTPOnly: another replica (or nobody, yet) owns the fleet.
	stayHTTPOnly
)

// decideWorkerStart is the single place that answers "should this process run
// the background worker fleet?", given how the leader lease probe went.
//
// A missing Redis client means leader election is configured but Redis is not
// wired at all (local dev, tests), so the process is the only one there is and
// must run the fleet. A *failed* probe against a configured Redis is a
// different thing entirely: it means we cannot tell whether another replica
// already owns the fleet, and starting every cron on every replica is far worse
// than starting none. Treating that error as "we are alone" is what turned one
// Upstash quota breach into cluster-wide Postgres slot exhaustion — every
// replica failed open, ran the full fleet, and the fleet stampeded the
// connection pool on 2026-10-08.
func decideWorkerStart(leaderElection, redisClientAvailable, won bool, acquireErr error) workerStartDecision {
	if !leaderElection || !redisClientAvailable {
		return startWorkerFleet
	}
	if acquireErr != nil {
		return stayHTTPOnly
	}
	if won {
		return startWorkerFleet
	}
	return stayHTTPOnly
}

func (app *Application) startWorkersOrElect() error {
	if app.cfg == nil || !app.cfg.Workers.LeaderElection {
		return app.initializeWorkers()
	}

	redisAvailable := app.container != nil &&
		app.container.RedisClient != nil &&
		app.container.RedisClient.Client() != nil

	if !redisAvailable {
		app.log.Warn("worker leader election on but Redis is unavailable — starting workers on this process")
		return app.initializeWorkers()
	}

	ctx, cancel := context.WithCancel(context.Background())
	app.leaderCancel = cancel

	app.leaderLock = cache.NewLeaderLock(app.container.RedisClient.Client(), workerLeaderTTL)

	won, err := app.leaderLock.TryAcquire(ctx)
	if decideWorkerStart(true, redisAvailable, won, err) == stayHTTPOnly {
		if err != nil {
			// Redis is configured but refusing us (quota exceeded, suspended
			// budget, pool timeout). Stay HTTP-only and let the retry loop below
			// pick up leadership once Redis recovers; the Redis health monitor
			// already alerts on this.
			app.log.Error("worker leader acquire failed — staying HTTP-only until Redis recovers",
				"error", err)
		} else {
			app.log.Info("another replica holds worker leadership — this process is HTTP-only")
		}
		go app.maintainWorkerLeadership(ctx)
		return nil
	}

	app.log.Info("acquired worker leadership — starting background workers")
	if err := app.initializeWorkers(); err != nil {
		app.leaderLock.Release(context.Background())
		return err
	}
	app.workerMu.Lock()
	app.workersStarted = true
	app.workerMu.Unlock()
	go app.maintainWorkerLeadership(ctx)
	return nil
}

func (app *Application) maintainWorkerLeadership(ctx context.Context) {
	renew := time.NewTicker(workerLeaderRenew)
	retry := time.NewTicker(workerLeaderRetry)
	defer renew.Stop()
	defer retry.Stop()

	// Last time a renewal succeeded. We just acquired leadership, so start the
	// clock now; transient renewal failures are tolerated until this lapses a
	// full TTL (see shouldStandDownAfterRenewFailure).
	lastRenewed := time.Now()

	for {
		select {
		case <-ctx.Done():
			if app.leaderLock != nil {
				app.leaderLock.Release(context.Background())
			}
			return
		case <-renew.C:
			app.workerMu.Lock()
			isLeader := app.workersStarted
			app.workerMu.Unlock()
			if !isLeader || app.leaderLock == nil {
				continue
			}
			if err := app.leaderLock.Renew(ctx); err != nil {
				if shouldStandDownAfterRenewFailure(err, lastRenewed, time.Now()) {
					app.log.Error("lost worker leadership — standing down", "error", err)
					app.stopJobWorkers()
					continue
				}
				app.log.Warn("leader renewal failed — tolerating while lease is still fresh",
					"error", err,
					"since_last_renewal", time.Since(lastRenewed).Round(time.Second).String())
				continue
			}
			lastRenewed = time.Now()
		case <-retry.C:
			app.workerMu.Lock()
			isLeader := app.workersStarted
			app.workerMu.Unlock()
			if isLeader || app.leaderLock == nil {
				continue
			}
			ok, err := app.leaderLock.TryAcquire(ctx)
			if err != nil {
				app.log.Warn("worker leader retry failed", "error", err)
				continue
			}
			if !ok {
				continue
			}
			app.log.Info("acquired worker leadership after failover — starting background workers")
			if err := app.initializeWorkers(); err != nil {
				app.log.Error("failed to start workers after becoming leader", "error", err)
				app.leaderLock.Release(context.Background())
				continue
			}
			app.workerMu.Lock()
			app.workersStarted = true
			app.workerMu.Unlock()
		}
	}
}

// stopJobWorkers stops crons without tearing down the Redis health monitor.
func (app *Application) stopJobWorkers() {
	app.stopWorkers()
	app.startRedisMonitor()
	app.workerMu.Lock()
	app.workersStarted = false
	app.workerMu.Unlock()
}

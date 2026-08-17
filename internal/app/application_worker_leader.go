package app

import (
	"context"
	"time"

	"github.com/rail-service/rail_service/internal/infrastructure/cache"
	"github.com/rail-service/rail_service/pkg/alerting"
)

const (
	workerLeaderTTL    = 30 * time.Second
	workerLeaderRenew  = 10 * time.Second
	workerLeaderRetry  = 5 * time.Second
)

func (app *Application) startRedisMonitor() {
	if app.container == nil || app.container.RedisClient == nil {
		return
	}
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

func (app *Application) startWorkersOrElect() error {
	if app.cfg == nil || !app.cfg.Workers.LeaderElection {
		return app.initializeWorkers()
	}
	if app.container == nil || app.container.RedisClient == nil || app.container.RedisClient.Client() == nil {
		app.log.Warn("worker leader election on but Redis is unavailable — starting workers on this process")
		return app.initializeWorkers()
	}

	app.leaderLock = cache.NewLeaderLock(app.container.RedisClient.Client(), workerLeaderTTL)
	ctx, cancel := context.WithCancel(context.Background())
	app.leaderCancel = cancel

	won, err := app.leaderLock.TryAcquire(ctx)
	if err != nil {
		app.log.Warn("worker leader acquire failed — starting workers on this process", "error", err)
		return app.initializeWorkers()
	}
	if won {
		app.log.Info("acquired worker leadership — starting background workers")
		if err := app.initializeWorkers(); err != nil {
			app.leaderLock.Release(context.Background())
			return err
		}
		app.workerMu.Lock()
		app.workersStarted = true
		app.workerMu.Unlock()
	} else {
		app.log.Info("another replica holds worker leadership — this process is HTTP-only")
	}
	go app.maintainWorkerLeadership(ctx)
	return nil
}

func (app *Application) maintainWorkerLeadership(ctx context.Context) {
	renew := time.NewTicker(workerLeaderRenew)
	retry := time.NewTicker(workerLeaderRetry)
	defer renew.Stop()
	defer retry.Stop()

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
				app.log.Error("lost worker leadership", "error", err)
				app.stopJobWorkers()
			}
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

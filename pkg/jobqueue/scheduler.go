package jobqueue

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

type ScheduledJob struct {
	Name     string
	Schedule string
	Handler  func(ctx context.Context) error
}

type JobScheduler struct {
	cron   *cron.Cron
	logger *zap.Logger
	jobs   map[string]cron.EntryID
}

func NewJobScheduler(logger *zap.Logger) *JobScheduler {
	return &JobScheduler{
		cron:   cron.New(cron.WithSeconds()),
		logger: logger,
		jobs:   make(map[string]cron.EntryID),
	}
}

func (js *JobScheduler) AddJob(job ScheduledJob) error {
	entryID, err := js.cron.AddFunc(job.Schedule, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		js.logger.Info("Executing scheduled job", zap.String("job", job.Name))
		if err := job.Handler(ctx); err != nil {
			js.logger.Error("Scheduled job failed", zap.String("job", job.Name), zap.Error(err))
		}
	})

	if err != nil {
		return err
	}

	js.jobs[job.Name] = entryID
	return nil
}

func (js *JobScheduler) RemoveJob(name string) {
	if entryID, exists := js.jobs[name]; exists {
		js.cron.Remove(entryID)
		delete(js.jobs, name)
	}
}

func (js *JobScheduler) Start() {
	js.cron.Start()
	js.logger.Info("Job scheduler started")
}

func (js *JobScheduler) Stop() {
	ctx := js.cron.Stop()
	<-ctx.Done()
	js.logger.Info("Job scheduler stopped")
}

func (js *JobScheduler) GetJobs() []string {
	names := make([]string, 0, len(js.jobs))
	for name := range js.jobs {
		names = append(names, name)
	}
	return names
}

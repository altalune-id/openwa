package message

import (
	"context"
	"log/slog"
	"time"

	"altalune.id/openwa/scheduler"
)

// RetentionJobName is the scheduler name of the nightly retention sweep.
const RetentionJobName = "message-retention"

const (
	retentionHour    = 3
	retentionTimeout = 20 * time.Minute
)

// Scheduler adapts *Service to scheduler.Provider.
type Scheduler struct {
	svc *Service
	log *slog.Logger
	loc scheduler.LocationFunc
}

// NewScheduler binds svc to the retention job, resolving its zone through loc (UTC when nil).
func NewScheduler(svc *Service, log *slog.Logger, loc scheduler.LocationFunc) *Scheduler {
	if loc == nil {
		loc = scheduler.FixedLocation(time.UTC)
	}
	return &Scheduler{svc: svc, log: log.With("module", "message"), loc: loc}
}

// SchedulerJobs implements scheduler.Provider; the runner fans the job out per org with a tenant-bound ctx.
func (a *Scheduler) SchedulerJobs() []scheduler.Job {
	return []scheduler.Job{{
		Name:      RetentionJobName,
		Scope:     scheduler.ScopeTenant,
		Schedule:  scheduler.MustDailyAt(retentionHour, 0, a.loc(RetentionJobName)),
		Timeout:   retentionTimeout,
		Singleton: true,
		Run:       func(ctx context.Context) error { return a.svc.RunRetention(ctx) },
	}}
}

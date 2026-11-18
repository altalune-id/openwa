package webhook

import (
	"context"
	"time"

	"altalune.id/openwa/scheduler"
)

// HintBackfillJobName is the scheduler name of the secret hint backfill.
const HintBackfillJobName = "webhook-secret-hint-backfill"

// Scheduler adapts *Service to scheduler.Provider.
type Scheduler struct{ svc *Service }

// NewScheduler binds svc to the secret hint backfill job.
func NewScheduler(svc *Service) *Scheduler { return &Scheduler{svc: svc} }

// SchedulerJobs implements scheduler.Provider; the runner fans the job out per org with a tenant-bound ctx.
func (a *Scheduler) SchedulerJobs() []scheduler.Job {
	return []scheduler.Job{{
		Name:      HintBackfillJobName,
		Scope:     scheduler.ScopeTenant,
		Schedule:  scheduler.MustEveryInterval(time.Hour, 0),
		Timeout:   time.Minute,
		Singleton: true,
		Run:       func(ctx context.Context) error { return a.svc.BackfillSecretHints(ctx) },
	}}
}

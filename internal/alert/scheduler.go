package alert

import (
	"time"

	"altalune.id/openwa/scheduler"
)

// DisconnectJobName is the scheduler name of the disconnect email job.
const DisconnectJobName = "alert-disconnect"

const disconnectTimeout = 30 * time.Second

// Scheduler adapts *Service to scheduler.Provider.
type Scheduler struct{ svc *Service }

// NewScheduler binds svc to the disconnect job.
func NewScheduler(svc *Service) *Scheduler { return &Scheduler{svc: svc} }

// SchedulerJobs implements scheduler.Provider; the runner fans the job out per org with a tenant-bound ctx and holds a lock per tick.
func (a *Scheduler) SchedulerJobs() []scheduler.Job {
	return []scheduler.Job{{
		Name:      DisconnectJobName,
		Scope:     scheduler.ScopeTenant,
		Schedule:  scheduler.MustEveryInterval(time.Minute, 0),
		Timeout:   disconnectTimeout,
		Singleton: true,
		Run:       a.svc.RunDisconnect,
	}}
}

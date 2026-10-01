package message

import (
	"time"

	"github.com/google/uuid"
)

// Retention bounds, in days.
const (
	DefaultRetentionDays = 30
	MinRetentionDays     = 1
	MaxRetentionDays     = 365
)

// RetentionPolicy is how long one project keeps its messages.
type RetentionPolicy struct {
	ProjectID uuid.UUID
	OrgID     uuid.UUID
	Days      int
	UpdatedAt time.Time
}

// NewRetentionPolicy validates days against 1..365.
func NewRetentionPolicy(orgID, projectID uuid.UUID, days int) (*RetentionPolicy, error) {
	if days < MinRetentionDays || days > MaxRetentionDays {
		return nil, &InvalidRetentionError{Days: days}
	}
	return &RetentionPolicy{ProjectID: projectID, OrgID: orgID, Days: days, UpdatedAt: time.Now().UTC()}, nil
}

// Cutoff is the instant before which messages are deleted.
func (p RetentionPolicy) Cutoff(now time.Time) time.Time { return now.UTC().AddDate(0, 0, -p.Days) }

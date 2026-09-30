package events

import (
	"time"

	"github.com/google/uuid"
)

// PostSnapshotV1 is the blog post shape shared by the publish and unpublish payloads.
type PostSnapshotV1 struct {
	ID               uuid.UUID   `json:"id"`
	Slug             string      `json:"slug"`
	Title            string      `json:"title"`
	BodyMarkdown     string      `json:"body_markdown"`
	CategoryID       uuid.UUID   `json:"category_id"`
	TagIDs           []uuid.UUID `json:"tag_ids"`
	FirstPublishedAt *time.Time  `json:"first_published_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
	Version          int         `json:"version"`
}

// PostPublishedV1 is the payload for PostPublished.
type PostPublishedV1 PostSnapshotV1

// PostUnpublishedV1 is the payload for PostUnpublished.
type PostUnpublishedV1 PostSnapshotV1

// PostDeletedV1 is the payload for PostDeleted.
type PostDeletedV1 struct {
	ID           uuid.UUID `json:"id"`
	Slug         string    `json:"slug"`
	WasPublished bool      `json:"was_published"`
}

// WebhookPingV1 is the payload for WebhookPing.
type WebhookPingV1 struct {
	EndpointID uuid.UUID `json:"endpoint_id"`
}

// DeviceRefV1 names the device a device event is about; ID is its public id, never the internal UUID.
type DeviceRefV1 struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

// DeviceEventV1 is the shape shared by the three device events.
type DeviceEventV1 struct {
	Device DeviceRefV1 `json:"device"`
	State  string      `json:"state"`
	Reason string      `json:"reason,omitempty"`
	At     time.Time   `json:"at"`
}

// DeviceConnectedV1 is the payload for DeviceConnected.
type DeviceConnectedV1 DeviceEventV1

// DeviceDisconnectedV1 is the payload for DeviceDisconnected.
type DeviceDisconnectedV1 DeviceEventV1

// DeviceLoggedOutV1 is the payload for DeviceLoggedOut.
type DeviceLoggedOutV1 DeviceEventV1

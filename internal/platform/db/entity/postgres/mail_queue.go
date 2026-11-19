package postgres

import "github.com/go-jet/jet/v2/postgres"

// MailQueue is the jet binding for the mail_queue table.
type MailQueue struct {
	postgres.Table

	ID            postgres.ColumnString
	Payload       postgres.ColumnBytea
	Status        postgres.ColumnString
	Attempt       postgres.ColumnInteger
	NextAttemptAt postgres.ColumnTimestampz
	LastError     postgres.ColumnString
	CreatedAt     postgres.ColumnTimestampz
	SettledAt     postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewMailQueue builds the mail_queue binding.
func NewMailQueue(schema, tablePrefix string) *MailQueue {
	if schema == "" {
		schema = "public"
	}
	var (
		id            = postgres.StringColumn("id")
		payload       = postgres.ByteaColumn("payload")
		status        = postgres.StringColumn("status")
		attempt       = postgres.IntegerColumn("attempt")
		nextAttemptAt = postgres.TimestampzColumn("next_attempt_at")
		lastError     = postgres.StringColumn("last_error")
		createdAt     = postgres.TimestampzColumn("created_at")
		settledAt     = postgres.TimestampzColumn("settled_at")
		all           = postgres.ColumnList{id, payload, status, attempt, nextAttemptAt, lastError, createdAt, settledAt}
	)
	return &MailQueue{
		Table:         postgres.NewTable(schema, tablePrefix+"mail_queue", "mail_queue", all...),
		ID:            id,
		Payload:       payload,
		Status:        status,
		Attempt:       attempt,
		NextAttemptAt: nextAttemptAt,
		LastError:     lastError,
		CreatedAt:     createdAt,
		SettledAt:     settledAt,
		AllColumns:    all,
	}
}

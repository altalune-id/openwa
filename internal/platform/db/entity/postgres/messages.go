package postgres

import "github.com/go-jet/jet/v2/postgres"

// Messages is the jet binding for the messages table.
type Messages struct {
	postgres.Table

	ID                postgres.ColumnString
	PublicID          postgres.ColumnString
	OrgID             postgres.ColumnString
	ProjectID         postgres.ColumnString
	DeviceID          postgres.ColumnString
	ChatID            postgres.ColumnString
	Direction         postgres.ColumnString
	WAMessageID       postgres.ColumnString
	SenderJID         postgres.ColumnString
	SenderLID         postgres.ColumnString
	SenderPhone       postgres.ColumnString
	SenderName        postgres.ColumnString
	FromMe            postgres.ColumnBool
	Type              postgres.ColumnString
	Body              postgres.ColumnString
	Media             postgres.ColumnString
	Location          postgres.ColumnString
	QuotedWAMessageID postgres.ColumnString
	Mentions          postgres.ColumnString
	TargetWAMessageID postgres.ColumnString
	Status            postgres.ColumnString
	Error             postgres.ColumnString
	Attempts          postgres.ColumnInteger
	WATimestamp       postgres.ColumnTimestampz
	SentAt            postgres.ColumnTimestampz
	DeliveredAt       postgres.ColumnTimestampz
	ReadAt            postgres.ColumnTimestampz
	Raw               postgres.ColumnBytea
	Version           postgres.ColumnInteger
	CreatedAt         postgres.ColumnTimestampz
	UpdatedAt         postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewMessages builds the messages binding.
func NewMessages(schema, tablePrefix string) *Messages {
	if schema == "" {
		schema = "public"
	}
	var (
		id                = postgres.StringColumn("id")
		publicID          = postgres.StringColumn("public_id")
		orgID             = postgres.StringColumn("org_id")
		projectID         = postgres.StringColumn("project_id")
		deviceID          = postgres.StringColumn("device_id")
		chatID            = postgres.StringColumn("chat_id")
		direction         = postgres.StringColumn("direction")
		waMessageID       = postgres.StringColumn("wa_message_id")
		senderJID         = postgres.StringColumn("sender_jid")
		senderLID         = postgres.StringColumn("sender_lid")
		senderPhone       = postgres.StringColumn("sender_phone")
		senderName        = postgres.StringColumn("sender_name")
		fromMe            = postgres.BoolColumn("from_me")
		typ               = postgres.StringColumn("type")
		body              = postgres.StringColumn("body")
		media             = postgres.StringColumn("media")
		location          = postgres.StringColumn("location")
		quotedWAMessageID = postgres.StringColumn("quoted_wa_message_id")
		mentions          = postgres.StringColumn("mentions")
		targetWAMessageID = postgres.StringColumn("target_wa_message_id")
		status            = postgres.StringColumn("status")
		errCol            = postgres.StringColumn("error")
		attempts          = postgres.IntegerColumn("attempts")
		waTimestamp       = postgres.TimestampzColumn("wa_timestamp")
		sentAt            = postgres.TimestampzColumn("sent_at")
		deliveredAt       = postgres.TimestampzColumn("delivered_at")
		readAt            = postgres.TimestampzColumn("read_at")
		raw               = postgres.ByteaColumn("raw")
		version           = postgres.IntegerColumn("version")
		createdAt         = postgres.TimestampzColumn("created_at")
		updatedAt         = postgres.TimestampzColumn("updated_at")
		all               = postgres.ColumnList{id, publicID, orgID, projectID, deviceID, chatID, direction, waMessageID, senderJID, senderLID, senderPhone, senderName, fromMe, typ, body, media, location, quotedWAMessageID, mentions, targetWAMessageID, status, errCol, attempts, waTimestamp, sentAt, deliveredAt, readAt, raw, version, createdAt, updatedAt}
	)
	return &Messages{
		Table:             postgres.NewTable(schema, tablePrefix+"messages", "messages", all...),
		ID:                id,
		PublicID:          publicID,
		OrgID:             orgID,
		ProjectID:         projectID,
		DeviceID:          deviceID,
		ChatID:            chatID,
		Direction:         direction,
		WAMessageID:       waMessageID,
		SenderJID:         senderJID,
		SenderLID:         senderLID,
		SenderPhone:       senderPhone,
		SenderName:        senderName,
		FromMe:            fromMe,
		Type:              typ,
		Body:              body,
		Media:             media,
		Location:          location,
		QuotedWAMessageID: quotedWAMessageID,
		Mentions:          mentions,
		TargetWAMessageID: targetWAMessageID,
		Status:            status,
		Error:             errCol,
		Attempts:          attempts,
		WATimestamp:       waTimestamp,
		SentAt:            sentAt,
		DeliveredAt:       deliveredAt,
		ReadAt:            readAt,
		Raw:               raw,
		Version:           version,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		AllColumns:        all,
	}
}

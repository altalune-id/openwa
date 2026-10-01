package events

import "time"

// ChatRefV1 names the chat a message belongs to.
type ChatRefV1 struct {
	ID   string `json:"id"`
	JID  string `json:"jid"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// SenderV1 is who sent a message; Phone is digits and empty when WhatsApp hides it.
type SenderV1 struct {
	JID      string `json:"jid"`
	LID      string `json:"lid"`
	Phone    string `json:"phone"`
	PushName string `json:"push_name"`
	FromMe   bool   `json:"from_me"`
}

// QuotedV1 is the message a reply quotes.
type QuotedV1 struct {
	WAID string `json:"wa_id"`
	Body string `json:"body"`
}

// MediaV1 describes an attachment; URL is the authenticated data-plane download.
type MediaV1 struct {
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	Voice    bool   `json:"voice"`
}

// LocationV1 is a shared map pin.
type LocationV1 struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Name    string  `json:"name"`
	Address string  `json:"address"`
}

// MessageV1 is the message itself; Mentions is never null.
type MessageV1 struct {
	ID        string      `json:"id"`
	WAID      string      `json:"wa_id"`
	Type      string      `json:"type"`
	Body      string      `json:"body"`
	Caption   string      `json:"caption"`
	Timestamp time.Time   `json:"timestamp"`
	Quoted    *QuotedV1   `json:"quoted"`
	Mentions  []string    `json:"mentions"`
	Media     *MediaV1    `json:"media"`
	Location  *LocationV1 `json:"location"`
}

// MatchedV1 lists why the device's inbound rules matched.
type MatchedV1 struct {
	Reasons []string `json:"reasons"`
}

// MessageEventV1 is the payload for MessageReceived and MessageMatched; Matched is set on the latter only.
type MessageEventV1 struct {
	Device  DeviceRefV1 `json:"device"`
	Chat    ChatRefV1   `json:"chat"`
	Sender  SenderV1    `json:"sender"`
	Message MessageV1   `json:"message"`
	Matched *MatchedV1  `json:"matched,omitempty"`
}

// MessageRefV1 names one message by our id and its WhatsApp id.
type MessageRefV1 struct {
	ID   string `json:"id"`
	WAID string `json:"wa_id"`
}

// MessageStatusV1 is the payload for MessageStatus: sent, delivered, read, played or failed.
type MessageStatusV1 struct {
	Device  DeviceRefV1  `json:"device"`
	Message MessageRefV1 `json:"message"`
	Status  string       `json:"status"`
	Error   string       `json:"error,omitempty"`
	At      time.Time    `json:"at"`
}

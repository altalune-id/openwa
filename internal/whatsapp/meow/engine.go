package meow

import (
	"cmp"
	"context"
	"log/slog"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"altalune.id/openwa/internal/whatsapp"
)

const defaultInboundQueueSize = 1024

// Options tunes the engine; InboundQueueSize is the per-session event buffer between whatsmeow and the sink.
type Options struct {
	InboundQueueSize int
	Log              *slog.Logger
}

// Engine is the whatsmeow implementation of whatsapp.Engine.
type Engine struct {
	c     *Container
	queue int
	log   *slog.Logger
}

var _ whatsapp.Engine = (*Engine)(nil)

// NewEngine builds the engine over the process's one container.
func NewEngine(c *Container, opts Options) *Engine {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Engine{c: c, queue: cmp.Or(opts.InboundQueueSize, defaultInboundQueueSize), log: log.With("engine", whatsapp.EngineWhatsmeow)}
}

// Name returns the engine name session rows record.
func (e *Engine) Name() string { return whatsapp.EngineWhatsmeow }

// Capabilities reports what whatsmeow implements.
func (e *Engine) Capabilities() whatsapp.Capabilities {
	return whatsapp.Capabilities{QRLink: true, PhoneCodeLink: true, Groups: true, HistorySync: true, Reactions: true, Edits: true}
}

// Open loads ref.JID's device and connects it, or prepares a fresh device for a link when ref.JID is empty.
func (e *Engine) Open(ctx context.Context, ref whatsapp.SessionRef, sink whatsapp.EventSink) (whatsapp.EngineSession, error) {
	var dev *store.Device
	if ref.JID == "" {
		dev = e.c.inner.NewDevice()
	} else {
		jid, err := types.ParseJID(ref.JID)
		if err != nil {
			return nil, &whatsapp.EngineError{Op: "open", Err: err}
		}
		dev, err = e.c.inner.GetDevice(ctx, jid)
		if err != nil {
			return nil, &whatsapp.EngineError{Op: "open", Err: err}
		}
		if dev == nil {
			return nil, &whatsapp.SessionGoneError{ID: ref.DeviceID.String()}
		}
	}
	log := e.log.With("device_id", ref.DeviceID)
	cli := whatsmeow.NewClient(dev, newLogger(log, "wa-client"))
	cli.EnableAutoReconnect = true
	cli.AutomaticMessageRerequestFromPhone = true
	cli.UseRetryMessageStore = true
	s := newSession(ref, clientAdapter{cli}, sink, e.queue, log)
	if ref.JID == "" {
		return s, nil
	}
	if err := cli.Connect(); err != nil {
		_ = s.Close(ctx)
		return nil, translate(ref.DeviceID, "connect", err)
	}
	return s, nil
}

// Purge deletes ref.JID's whatsmeow device and every per-device row; an absent device is not an error.
func (e *Engine) Purge(ctx context.Context, ref whatsapp.SessionRef) error {
	if ref.JID == "" {
		return nil
	}
	jid, err := types.ParseJID(ref.JID)
	if err != nil {
		return &whatsapp.EngineError{Op: "purge", Err: err}
	}
	dev, err := e.c.inner.GetDevice(ctx, jid)
	if err != nil {
		return &whatsapp.EngineError{Op: "purge", Err: err}
	}
	if dev == nil {
		return nil
	}
	if err := e.c.inner.DeleteDevice(ctx, dev); err != nil {
		return &whatsapp.EngineError{Op: "purge", Err: err}
	}
	return nil
}

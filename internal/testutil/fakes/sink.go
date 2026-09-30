package fakes

import (
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/whatsapp"
)

// SinkCall is one recorded EventSink call.
type SinkCall struct {
	Kind     string
	Ref      whatsapp.SessionRef
	State    whatsapp.State
	Reason   string
	Identity whatsapp.Identity
	Err      error
	Message  whatsapp.InboundMessage
	Receipt  whatsapp.Receipt
	Contact  whatsapp.ContactUpdate
	History  []whatsapp.InboundMessage
}

// Sink records every call; when Gate is set, OnState blocks on it after recording.
type Sink struct {
	mu    sync.Mutex
	calls []SinkCall
	Gate  chan struct{}
}

var _ whatsapp.EventSink = (*Sink)(nil)

func (s *Sink) add(c SinkCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
}

func (s *Sink) OnState(ref whatsapp.SessionRef, st whatsapp.State, reason string) {
	s.add(SinkCall{Kind: "state", Ref: ref, State: st, Reason: reason})
	if s.Gate != nil {
		<-s.Gate
	}
}

func (s *Sink) OnDegraded(ref whatsapp.SessionRef, err error) {
	s.add(SinkCall{Kind: "degraded", Ref: ref, Err: err})
}

func (s *Sink) OnLinked(ref whatsapp.SessionRef, id whatsapp.Identity) {
	s.add(SinkCall{Kind: "linked", Ref: ref, Identity: id})
}

func (s *Sink) OnMessage(ref whatsapp.SessionRef, m whatsapp.InboundMessage) {
	s.add(SinkCall{Kind: "message", Ref: ref, Message: m})
}

func (s *Sink) OnReceipt(ref whatsapp.SessionRef, r whatsapp.Receipt) {
	s.add(SinkCall{Kind: "receipt", Ref: ref, Receipt: r})
}

func (s *Sink) OnContact(ref whatsapp.SessionRef, c whatsapp.ContactUpdate) {
	s.add(SinkCall{Kind: "contact", Ref: ref, Contact: c})
}

func (s *Sink) OnHistory(ref whatsapp.SessionRef, batch []whatsapp.InboundMessage) {
	s.add(SinkCall{Kind: "history", Ref: ref, History: batch})
}

// Calls returns a copy of every recorded call.
func (s *Sink) Calls() []SinkCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SinkCall{}, s.calls...)
}

// States returns the OnState calls for id in order, as "state:reason".
func (s *Sink) States(id uuid.UUID) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		if c.Kind == "state" && c.Ref.DeviceID == id {
			out = append(out, string(c.State)+":"+c.Reason)
		}
	}
	return out
}

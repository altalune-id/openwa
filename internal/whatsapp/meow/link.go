package meow

import (
	"context"
	"errors"
	"time"

	"go.mau.fi/whatsmeow"

	"altalune.id/openwa/internal/whatsapp"
)

const (
	linkBuffer  = 8
	firstQRWait = 15 * time.Second
)

// Link asks for the QR channel before connecting, as whatsmeow requires, and feeds its items as LinkEvents until a terminal one. NOTE: the QR channel is bound to the session lifetime, not ctx, because whatsmeow disconnects the client when that context ends.
func (s *session) Link(ctx context.Context) (<-chan whatsapp.LinkEvent, error) {
	if s.isClosed() {
		return nil, &whatsapp.NotConnectedError{ID: s.deviceID.String()}
	}
	qr, err := s.cli.GetQRChannel(s.life)
	if err != nil {
		return nil, translate(s.deviceID, "link", err)
	}
	if err := s.cli.Connect(); err != nil {
		return nil, translate(s.deviceID, "connect", err)
	}
	out := make(chan whatsapp.LinkEvent, linkBuffer)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		// NOTE: Close may have disconnected before this Connect; disconnect again or the socket leaks.
		s.cli.Disconnect()
		return nil, &whatsapp.NotConnectedError{ID: s.deviceID.String()}
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer close(out)
		s.feedLink(qr, out)
	}()
	return out, nil
}

func (s *session) feedLink(qr <-chan whatsmeow.QRChannelItem, out chan<- whatsapp.LinkEvent) {
	for {
		select {
		case <-s.done:
			return
		case item, ok := <-qr:
			if !ok {
				return
			}
			ev := mapQRItem(item, time.Now())
			if ev.Kind == whatsapp.LinkEventCode {
				s.qrOnce.Do(func() { close(s.qrSeen) })
			}
			select {
			case out <- ev:
			case <-s.done:
				return
			}
			if ev.Kind != whatsapp.LinkEventCode {
				return
			}
		}
	}
}

func mapQRItem(item whatsmeow.QRChannelItem, now time.Time) whatsapp.LinkEvent {
	switch item.Event {
	case whatsmeow.QRChannelEventCode:
		return whatsapp.LinkEvent{Kind: whatsapp.LinkEventCode, Code: item.Code, Expires: now.Add(item.Timeout)}
	case whatsmeow.QRChannelSuccess.Event:
		return whatsapp.LinkEvent{Kind: whatsapp.LinkEventSuccess}
	case whatsmeow.QRChannelTimeout.Event:
		return whatsapp.LinkEvent{Kind: whatsapp.LinkEventTimeout}
	case whatsmeow.QRChannelEventPasskeyRequest, whatsmeow.QRChannelEventPasskeyResponse:
		return whatsapp.LinkEvent{Kind: whatsapp.LinkEventUnsupported, Err: &whatsapp.UnsupportedError{Feature: "passkey pairing"}}
	case whatsmeow.QRChannelEventError:
		err := item.Error
		if err == nil {
			err = errors.New("pairing failed")
		}
		return whatsapp.LinkEvent{Kind: whatsapp.LinkEventError, Err: err}
	}
	return whatsapp.LinkEvent{Kind: whatsapp.LinkEventError, Err: errors.New("whatsmeow: " + item.Event)}
}

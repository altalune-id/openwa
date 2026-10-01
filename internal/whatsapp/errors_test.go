package whatsapp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/whatsapp"
)

func TestFailureClassNeverCarriesRawText(t *testing.T) {
	require.Equal(t, "reachout_timelock", whatsapp.FailureClass(&whatsapp.EngineError{Op: "send", Reason: "reachout_timelock", Err: errors.New("server returned 463 for +62811")}))
	require.Equal(t, "engine_error", whatsapp.FailureClass(errors.New("dial tcp 10.1.2.3:443: refused")))
	require.Equal(t, "not_connected", whatsapp.FailureClass(&whatsapp.NotConnectedError{ID: "d"}))
	require.Equal(t, "cancelled", whatsapp.FailureClass(context.Canceled))
}

func TestFailureClassCoversEveryKind(t *testing.T) {
	cases := map[string]error{
		"unknown":           nil,
		"not_owned":         &whatsapp.NotOwnedError{ID: "d"},
		"unsupported":       &whatsapp.UnsupportedError{Feature: "x"},
		"invalid_recipient": &whatsapp.InvalidJIDError{Raw: "x"},
		"media_unavailable": &whatsapp.MediaUnavailableError{ID: "d"},
	}
	for want, err := range cases {
		require.Equal(t, want, whatsapp.FailureClass(err))
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{context.Canceled, true},
		{&whatsapp.NotConnectedError{ID: "d"}, true},
		{&whatsapp.EngineError{Op: "send", Reason: "ack_timeout", Retryable: true}, true},
		{&whatsapp.EngineError{Op: "send", Reason: "reachout_timelock"}, false},
		{&whatsapp.UnsupportedError{Feature: "x"}, false},
		{&whatsapp.InvalidJIDError{Raw: "x"}, false},
		{&whatsapp.MediaUnavailableError{ID: "d"}, false},
		{errors.New("connection reset"), true},
	}
	for _, tt := range cases {
		require.Equal(t, tt.want, whatsapp.IsRetryable(tt.err), "%v", tt.err)
	}
}

func TestEngineErrorMessageFoldsInTheReason(t *testing.T) {
	require.Equal(t, "whatsapp: engine send: ack_timeout: boom", (&whatsapp.EngineError{Op: "send", Reason: "ack_timeout", Err: errors.New("boom")}).Error())
	require.Equal(t, "whatsapp: engine send: boom", (&whatsapp.EngineError{Op: "send", Err: errors.New("boom")}).Error())
	require.Contains(t, (&whatsapp.MediaUnavailableError{ID: "d"}).Error(), "d")
	require.Contains(t, (&whatsapp.SetupError{Reason: "r"}).Error(), "r")
}

package whatsapp

import (
	"context"
	"log/slog"
)

// NewTestSender exposes the unexported sender to the package's external tests.
func NewTestSender(ref SessionRef, sess EngineSession, out Outbound, wake <-chan struct{}, cfg SenderConfig, log *slog.Logger) func(context.Context) {
	s := &sender{ref: ref, sess: sess, out: out, wake: wake, cfg: cfg, log: log}
	return s.run
}

// MarkTimeout exposes the budget of one outcome write to the external tests.
const MarkTimeout = markTimeout

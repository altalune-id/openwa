package meow

import (
	"fmt"
	"log/slog"

	waLog "go.mau.fi/whatsmeow/util/log"
)

type slogLogger struct {
	log *slog.Logger
	mod string
}

func newLogger(log *slog.Logger, mod string) waLog.Logger {
	if log == nil {
		log = slog.Default()
	}
	return &slogLogger{log: log, mod: mod}
}

func (l *slogLogger) Warnf(msg string, args ...any) {
	l.log.Warn(fmt.Sprintf(msg, args...), "wa_module", l.mod)
}
func (l *slogLogger) Errorf(msg string, args ...any) {
	l.log.Error(fmt.Sprintf(msg, args...), "wa_module", l.mod)
}
func (l *slogLogger) Infof(msg string, args ...any) {
	l.log.Info(fmt.Sprintf(msg, args...), "wa_module", l.mod)
}
func (l *slogLogger) Debugf(msg string, args ...any) {
	l.log.Debug(fmt.Sprintf(msg, args...), "wa_module", l.mod)
}
func (l *slogLogger) Sub(module string) waLog.Logger {
	return &slogLogger{log: l.log, mod: l.mod + "/" + module}
}

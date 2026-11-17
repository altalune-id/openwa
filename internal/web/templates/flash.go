package templates

import "altalune.id/openwa/internal/web"

func flashClass(kind web.FlashKind) string {
	switch kind {
	case web.FlashOK:
		return "alert alert-soft alert-success"
	case web.FlashWarn:
		return "alert alert-soft alert-warning"
	case web.FlashErr:
		return "alert alert-soft alert-error"
	case web.FlashInfo:
		return "alert alert-soft alert-info"
	}
	return "alert alert-soft"
}

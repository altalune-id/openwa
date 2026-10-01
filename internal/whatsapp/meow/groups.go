package meow

import (
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"altalune.id/openwa/internal/whatsapp"
)

func toGroupInfo(g *types.GroupInfo) whatsapp.GroupInfo {
	return whatsapp.GroupInfo{
		JID:          g.JID.String(),
		Name:         g.Name,
		Topic:        g.Topic,
		Participants: max(g.ParticipantCount, len(g.Participants)),
		Announce:     g.IsAnnounce,
		Locked:       g.IsLocked,
	}
}

func inviteCode(link string) (string, error) {
	code := strings.TrimSpace(link)
	for _, prefix := range []string{whatsmeow.InviteLinkPrefix, "http://chat.whatsapp.com/", "chat.whatsapp.com/"} {
		code = strings.TrimPrefix(code, prefix)
	}
	if code == "" || strings.ContainsAny(code, "/:. \t") {
		return "", &whatsapp.InvalidJIDError{Raw: link}
	}
	return code, nil
}

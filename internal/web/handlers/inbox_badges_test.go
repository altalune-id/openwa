package handlers_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
)

func (x *inboxFixture) inbound(t *testing.T, chatID uuid.UUID, body string, outcome message.MatchOutcome, ruleID string) *message.Message {
	t.Helper()
	m := &message.Message{
		ID: uuid.Must(uuid.NewV7()), PublicID: "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16], OrgID: x.tc.OrgID, ProjectID: x.tc.ProjectID,
		DeviceID: x.device, ChatID: chatID, Direction: message.DirectionIn, WAMessageID: uuid.NewString(), SenderJID: "12345678901234@lid",
		SenderLID: "12345678901234@lid", Type: message.TypeText, Body: body, WATimestamp: time.Now().UTC(), MatchOutcome: outcome, MatchedRuleID: ruleID, Version: 1,
	}
	x.Store.Seed(m)
	return m
}

func badgeOf(t *testing.T, body, publicID string) string {
	t.Helper()
	i := strings.Index(body, `id="msg-`+publicID+`"`)
	require.GreaterOrEqual(t, i, 0, "bubble %s rendered", publicID)
	j := strings.Index(body[i+1:], `<li id="msg-`)
	if j < 0 {
		return body[i:]
	}
	return body[i : i+1+j]
}

func TestInbox_MatchBadgesSayWhatTheRulesDid(t *testing.T) {
	x := newInboxFixture(t)
	c, sent := x.chatWith(t, "from us")
	matched := x.inbound(t, c.ID, "order 3", message.MatchOutcome("matched"), "legacy-dm")
	sentGone := x.inbound(t, c.ID, "old order", message.MatchOutcome("matched"), "deleted-rule")
	ignored := x.inbound(t, c.ID, "promo", message.MatchOutcome("ignored"), "gone")
	none := x.inbound(t, c.ID, "hi", message.MatchOutcome("none"), "")
	imported := x.inbound(t, c.ID, "old", "", "")

	rec := x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID, "", false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	rulesHref := `href="/orgs/acme/projects/alpha/devices/` + x.devicePub + `/rules`

	b := badgeOf(t, body, matched.PublicID)
	assert.Contains(t, b, "inbox.match.sent")
	assert.Contains(t, b, "inbox.match.sent_rule")
	assert.Contains(t, b, rulesHref+`/legacy-dm"`)

	b = badgeOf(t, body, ignored.PublicID)
	assert.Contains(t, b, "inbox.match.not_sent")
	assert.Contains(t, b, "inbox.match.ignored_rule_gone", "a deleted Don't send rule is said without the word matched")
	assert.NotContains(t, b, "inbox.match.rule_gone\"")

	b = badgeOf(t, body, sentGone.PublicID)
	assert.Contains(t, b, "inbox.match.sent")
	assert.Contains(t, b, "inbox.match.sent_rule_gone")

	b = badgeOf(t, body, none.PublicID)
	assert.Contains(t, b, "inbox.match.no_rule")
	assert.Contains(t, b, rulesHref+`"`)

	b = badgeOf(t, body, imported.PublicID)
	assert.Contains(t, b, "inbox.match.not_evaluated", "a history import is not evaluated, never not sent")
	assert.NotContains(t, b, "inbox.match.not_sent")
	assert.NotContains(t, b, rulesHref, "no Edit rules link on a message the rules never saw")

	b = badgeOf(t, body, sent[0].PublicID)
	assert.NotContains(t, b, "inbox.match.", "an outbound message gets no badge")
	assert.NotContains(t, body, "@lid", "no raw @lid anywhere in the thread")
}

func TestInbox_OnPhonesTheThreadIsItsOwnView(t *testing.T) {
	x := newInboxFixture(t)
	c, _ := x.chatWith(t, "halo")
	page := x.do(t, http.MethodGet, inboxBase, "", false).Body.String()
	assert.Contains(t, page, "max-lg:group-has-[[data-thread]]/inbox:hidden", "the list hides on phones once a thread is in the pane")
	thread := x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID, "", false).Body.String()
	assert.Contains(t, thread, `aria-label="inbox.back_to_chats"`)
	assert.Contains(t, thread, `data-thread`, "the pane marks itself for the phone-only list hide")
}

func TestInbox_AnUnknownOutcomeReadsNotEvaluated(t *testing.T) {
	x := newInboxFixture(t)
	c, _ := x.chatWith(t, "hi")
	odd := x.inbound(t, c.ID, "future", message.MatchOutcome("deferred"), "legacy-dm")
	body := x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID, "", false).Body.String()
	b := badgeOf(t, body, odd.PublicID)
	assert.Contains(t, b, "inbox.match.not_evaluated")
	assert.NotContains(t, b, "inbox.match.sent")
}

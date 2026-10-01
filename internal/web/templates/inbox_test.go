package templates

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var hxTag = regexp.MustCompile(`<[a-z]+[^>]*\shx-(get|post|target|swap|trigger|disable|encoding)=[^>]*>`)

func inboxThreadFixture() ThreadView {
	at := time.Now()
	return ThreadView{
		ProjectSlug: "alpha", ChatID: "cht_x", Name: "Ani", JID: "62811@s.whatsapp.net", Kind: "group",
		Group: &GroupSummary{Participants: 3}, OlderCursor: "c", LastID: "t", Watermark: "w", MaxMediaMB: 1,
		Reactions: []string{"👍"},
		Bubbles:   []BubbleView{{ID: "msg_a", WAID: "A", Outbound: true, Own: true, Status: "sent", At: at, Body: "hi"}},
		Refresh:   []BubbleView{{ID: "msg_b", WAID: "B", At: at, Body: "yo"}},
	}
}

func TestInbox_EveryHTMXElementCarriesTheNonce(t *testing.T) {
	d := uiData()
	v := inboxThreadFixture()
	list := ChatListView{ProjectSlug: "alpha", Limit: 50, HasMore: true, Rows: []ChatRowView{{ID: "cht_x", Name: "Ani"}}}
	pages := map[string]string{
		"thread":   render(t, ThreadPane(d, v)),
		"append":   render(t, ThreadAppend(d, v)),
		"prepend":  render(t, ThreadPrepend(d, v)),
		"composer": render(t, ComposerReset(d, v)),
		"sidebar":  render(t, ChatListSidebar(d, []InboxDevice{{ID: "dev_x", Name: "s"}}, list)),
		"opened":   render(t, ThreadOpened(d, v, list)),
	}
	for name, html := range pages {
		for _, tag := range hxTag.FindAllString(html, -1) {
			assert.Contains(t, tag, `hx-nonce="n0nce"`, "%s: %s", name, tag)
		}
		assert.Equal(t, 0, strings.Count(html, "<script"), name)
	}
}

func TestInbox_ThreadOpensAtTheNewestAndPollScrollsToIt(t *testing.T) {
	html := render(t, ThreadPane(uiData(), inboxThreadFixture()))
	assert.Regexp(t, `id="thread-scroll"[^>]*class="[^"]*flex-col-reverse`, html, "a reversed flex column starts at its bottom")
	assert.Contains(t, html, `hx-swap="beforeend scroll:bottom scrollTarget:#thread-scroll"`)
}

package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
)

const (
	tokoJID  = "120363001@g.us"
	sariJID  = "6281111222333@s.whatsapp.net"
	rinaJID  = "6289999888777@s.whatsapp.net"
	hiddenID = "12345678901234@lid"
)

type rulesFixture struct {
	*deviceFixture
	dev       *device.Device
	chatStore *fakes.Chat
	toko      *chat.Chat
	other     *chat.Chat
	sariDM    *chat.Chat
	rinaDM    *chat.Chat
	Messages  *fakes.Message
}

func newRulesFixture(t *testing.T) *rulesFixture {
	t.Helper()
	x := newDeviceFixture(t)
	d := x.seed(t, "Toko Sari Admin")
	tc := tenant.Context{OrgID: x.org, ProjectID: x.project, UserID: x.uid}

	chatStore := fakes.NewChat()
	chats := chat.NewService(chatStore, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, &fakes.Groups{})
	seedChat := func(jid, name string, kind chat.Kind) *chat.Chat {
		c, err := chat.New(tc.OrgID, tc.ProjectID, d.ID, fakes.ChatPublicID(), jid, "", kind)
		require.NoError(t, err)
		c.Name = name
		chatStore.Seed(c)
		return c
	}
	contactStore := fakes.NewContact()
	contacts := contact.NewService(contactStore, discardLogger(), passthroughUnexpected())
	seedContact := func(jid, lid, phone, push string) {
		c := contact.New(tc.OrgID, tc.ProjectID, d.ID, jid)
		c.LID, c.Phone, c.PushName = lid, phone, push
		contactStore.Seed(c)
	}
	seedContact(sariJID, "", "6281111222333", "Sari Wulandari")
	seedContact(rinaJID, "", "6289999888777", "Rina")
	seedContact(hiddenID, hiddenID, "", "Hidden Member")
	seedContact("55555@lid", "55555@lid", "", "")

	store := fakes.NewMessage()
	msgs := message.NewService(store, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, message.Deps{
		Devices: &fakes.MessageDevices{}, Chats: chatsPort{svc: chats}, Contacts: &fakes.MessageContacts{},
		Transport: &fakes.Transport{}, Media: fakes.MediaStore{Transport: &fakes.Transport{}}, Fetcher: &fakes.MediaFetcher{},
		Waker: &fakes.Waker{}, Webhooks: &fakes.Webhooks{}, Tenants: &fakes.MessageTenants{Org: "acme", Project: "alpha"},
	}, message.Options{BaseURL: "http://localhost", MaxMediaBytes: 1 << 20, StaleAfter: time.Minute})

	otherDev := x.seed(t, "Other number")
	other, err := chat.New(tc.OrgID, tc.ProjectID, otherDev.ID, fakes.ChatPublicID(), "120363999@g.us", "", chat.KindGroup)
	require.NoError(t, err)
	chatStore.Seed(other)

	deviceHandler := handlers.NewDeviceHandler(x.Deps, x.Projects, x.Devices, msgs)
	handlers.NewRulesHandler(deviceHandler, msgs, chats, contacts).Register(x.Mux)
	return &rulesFixture{
		deviceFixture: x, dev: d, Messages: store, other: other, chatStore: chatStore,
		toko:   seedChat(tokoJID, "Toko Admin", chat.KindGroup),
		sariDM: seedChat(sariJID, "Sari Wulandari", chat.KindDM),
		rinaDM: seedChat(rinaJID, "Rina", chat.KindDM),
	}
}

func (x *rulesFixture) path(suffix string) string {
	return devicesBase + "/" + x.dev.PublicID + "/rules" + suffix
}

func (x *rulesFixture) stored(t *testing.T) *device.Device {
	t.Helper()
	got, err := x.Devices.Get(setTenantProject(context.Background(), x.org, x.project, x.uid), x.dev.ID)
	require.NoError(t, err)
	return got.Device
}

func tokoAndSari(version int) url.Values {
	return url.Values{
		"version": {strconv.Itoa(version)}, "name": {"Toko Admin and Sari"}, "action": {"match"},
		"groups": {"these"}, "chat": {tokoJID}, "who": {"only"}, "sender": {"phone:6281111222333"},
	}
}

func (x *rulesFixture) post(t *testing.T, suffix string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	return x.do(t, http.MethodPost, x.path(suffix), form.Encode(), htmx)
}

func TestRules_BuildTokoAdminAndSari(t *testing.T) {
	x := newRulesFixture(t)
	rec := x.do(t, http.MethodGet, x.path("/toko"), "", false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `id="rule-picker"`, "the picker dialog sits in the body-level host")

	rec = x.post(t, "/toko", tokoAndSari(1), false)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	require.Equal(t, x.path(""), rec.Header().Get("Location"))

	got := x.stored(t)
	require.True(t, got.Rules.IgnoreFromMe, "the hidden from-me flag is kept")
	toko := got.Rules.Items[len(got.Rules.Items)-1]
	require.Equal(t, "toko", toko.ID)
	require.Equal(t, []device.Condition{
		{Field: device.FieldChatKind, Op: device.OpIs, Values: []string{"group"}},
		{Field: device.FieldChat, Op: device.OpIs, Values: []string{tokoJID}},
		{Field: device.FieldSender, Op: device.OpIs, Values: []string{"phone:6281111222333"}},
	}, toko.Conditions)

	list := x.do(t, http.MethodGet, x.path(""), "", false).Body.String()
	assert.Contains(t, list, "Toko Admin and Sari")
	assert.Contains(t, list, "rules.sentence.full", "the row carries the rule sentence")
	assert.Contains(t, list, "rules.everything_else")
}

func TestRules_TestBoxAnswersTheWalkFourOfFour(t *testing.T) {
	x := newRulesFixture(t)
	draft := tokoAndSari(1)
	draft.Del("version")
	run := func(chatID, sender string) string {
		t.Helper()
		f := url.Values{}
		for k, v := range draft {
			f[k] = v
		}
		f.Set("test_chat", chatID)
		f.Set("test_sender", sender)
		f.Set("test_text", "Mau order 2 lusin")
		rec := x.post(t, "/toko/test", f, true)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec.Body.String()
	}
	assert.Contains(t, run(x.toko.PublicID, sariJID), "rules.test.sent", "wife in the group")
	assert.Contains(t, run(x.toko.PublicID, rinaJID), "rules.test.not_only", "courier in the group")
	wifeDM := run(x.sariDM.PublicID, rinaJID)
	assert.Contains(t, wifeDM, "rules.test.dm_off", "wife in a DM")
	assert.Contains(t, wifeDM, `id="rule-test-sender" name="test_sender" class="select w-full lg:select-sm" disabled`, "a DM locks the sender")
	assert.Contains(t, run(x.rinaDM.PublicID, sariJID), "rules.test.dm_off", "courier in a DM: the locked sender is the chat, not the posted one")
	assert.Equal(t, 1, x.stored(t).Version, "the test box never writes")
}

func TestRules_TestBoxLocksADMSenderToTheChat(t *testing.T) {
	x := newRulesFixture(t)
	f := url.Values{"dm": {"1"}, "groups": {"none"}, "who": {"only"}, "sender": {"phone:6281111222333"}, "action": {"match"},
		"test_chat": {x.rinaDM.PublicID}, "test_sender": {sariJID}}
	rec := x.post(t, "/dm/test", f, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, testResultText(rec.Body.String()), "rules.test.not_only", "Rina's DM is from Rina, whatever sender was posted")
}

func TestRules_TestBoxRefusesAnotherNumbersChat(t *testing.T) {
	x := newRulesFixture(t)
	f := tokoAndSari(1)
	f.Set("test_chat", x.other.PublicID)
	f.Set("test_sender", sariJID)
	rec := x.post(t, "/toko/test", f, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, testResultText(rec.Body.String()), "rules.test.pick_chat")
}

func TestRules_TestBoxMentionIsUntickedByDefault(t *testing.T) {
	x := newRulesFixture(t)
	body := x.do(t, http.MethodGet, x.path("/toko"), "", false).Body.String()
	require.Contains(t, body, `name="test_mention"`)
	require.NotContains(t, body, `name="test_mention" value="1" checked`)
}

func TestRules_LIDOnlyMemberMatchesAndAPhoneOfItsDigitsDoesNot(t *testing.T) {
	x := newRulesFixture(t)
	run := func(sender string) string {
		t.Helper()
		f := url.Values{"groups": {"all"}, "who": {"only"}, "sender": {sender}, "action": {"match"},
			"test_chat": {x.toko.PublicID}, "test_sender": {hiddenID}}
		rec := x.post(t, "/lid/test", f, true)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec.Body.String()
	}
	assert.Contains(t, run(hiddenID), "rules.test.sent")
	digits := run("phone:12345678901234")
	assert.Contains(t, digits, "rules.test.not_only")
	assert.NotContains(t, testResultText(digits), "@lid", "a reason never shows a raw @lid")
}

func TestRules_DMWithGroupsSavesAPairActedOnTogether(t *testing.T) {
	x := newRulesFixture(t)
	f := tokoAndSari(1)
	f.Set("dm", "1")
	require.Equal(t, http.StatusSeeOther, x.post(t, "/orders", f, false).Code)
	got := x.stored(t)
	ids := ruleIDs(got.Rules.Items)
	require.Equal(t, []string{"legacy-dm", "legacy-group", "orders", "orders-dm"}, ids)

	list := x.do(t, http.MethodGet, x.path(""), "", false).Body.String()
	assert.Equal(t, 1, strings.Count(list, `id="rule-orders"`))
	assert.NotContains(t, list, `id="rule-orders-dm"`, "a pair is one row")

	rec := x.post(t, "/orders/toggle", url.Values{"version": {"2"}}, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got = x.stored(t)
	require.Equal(t, 3, got.Version, "one write for both halves")
	require.False(t, got.Rules.Items[2].Enabled)
	require.False(t, got.Rules.Items[3].Enabled)
	assert.Contains(t, rec.Body.String(), `value="3"`, "the swapped list carries the new version")

	require.Equal(t, http.StatusOK, x.post(t, "/orders/move", url.Values{"version": {"3"}, "to": {"up"}}, true).Code)
	require.Equal(t, []string{"legacy-dm", "orders", "orders-dm", "legacy-group"}, ruleIDs(x.stored(t).Rules.Items))

	require.Equal(t, http.StatusOK, x.post(t, "/orders/delete", url.Values{"version": {"4"}}, true).Code)
	require.Equal(t, []string{"legacy-dm", "legacy-group"}, ruleIDs(x.stored(t).Rules.Items))
}

func TestRules_HitsSumBothHalves(t *testing.T) {
	x := newRulesFixture(t)
	f := tokoAndSari(1)
	f.Set("dm", "1")
	require.Equal(t, http.StatusSeeOther, x.post(t, "/orders", f, false).Code)
	at := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	for i, id := range []string{"orders", "orders-dm", "orders-dm"} {
		x.Messages.Seed(&message.Message{
			ID: uuid.New(), OrgID: x.org, ProjectID: x.project, DeviceID: x.dev.ID, Direction: message.DirectionIn,
			WATimestamp: at.Add(time.Duration(i) * time.Hour), MatchOutcome: message.MatchOutcome(device.OutcomeMatched), MatchedRuleID: id,
		})
	}
	list := x.do(t, http.MethodGet, x.path(""), "", false).Body.String()
	assert.Contains(t, list, "rules.matched")
	assert.Contains(t, list, `datetime="2026-11-01T05:00:00Z"`, "last matched is the later of the two halves")
}

func TestRules_StaleSaveIsAConflictAndKeepsTheItems(t *testing.T) {
	x := newRulesFixture(t)
	require.Equal(t, http.StatusSeeOther, x.post(t, "/a", tokoAndSari(1), false).Code)
	before := x.stored(t).Rules.Items

	rec := x.post(t, "/b", tokoAndSari(1), false)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "rules.conflict")
	assert.Contains(t, rec.Body.String(), `value="Toko Admin and Sari"`, "the draft is kept")
	require.Equal(t, before, x.stored(t).Rules.Items)

	rec = x.post(t, "/a/toggle", url.Values{"version": {"1"}}, true)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, before, x.stored(t).Rules.Items)
}

func TestRules_InvalidDraftNamesTheBlock(t *testing.T) {
	x := newRulesFixture(t)
	f := tokoAndSari(1)
	f.Del("chat")
	rec := x.post(t, "/a", f, false)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "rules.error.where_groups")
	assert.Contains(t, rec.Body.String(), "border-error bg-error/10")
	assert.Equal(t, 1, x.stored(t).Version)
}

func TestRules_CustomRuleIsReadOnlyButTestable(t *testing.T) {
	x := newRulesFixture(t)
	api := device.Rules{IgnoreFromMe: true, Items: []device.Rule{{ID: "api", Name: "API", Enabled: true, Action: device.ActionMatch,
		Conditions: []device.Condition{{Field: device.FieldChatKind, Op: device.OpIsNot, Values: []string{"group"}}}}}}
	_, err := x.Devices.Update(setTenantProject(context.Background(), x.org, x.project, x.uid), x.dev.ID, device.Update{Rules: &api}, 0)
	require.NoError(t, err)

	list := x.do(t, http.MethodGet, x.path(""), "", false).Body.String()
	assert.Contains(t, list, "rules.custom_summary")
	page := x.do(t, http.MethodGet, x.path("/api"), "", false).Body.String()
	assert.Contains(t, page, "rules.custom_read_only")
	assert.NotContains(t, page, "data-rule-save")

	rec := x.post(t, "/api", tokoAndSari(2), false)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, api.Items, x.stored(t).Rules.Items, "a hand-made save never overwrites a custom rule")

	rec = x.post(t, "/api/test", url.Values{"test_chat": {x.sariDM.PublicID}}, true)
	assert.Contains(t, rec.Body.String(), "rules.test.sent")
}

func TestRules_AllDeletedSendsNothing(t *testing.T) {
	x := newRulesFixture(t)
	require.Equal(t, http.StatusSeeOther, x.post(t, "/legacy-dm/delete", url.Values{"version": {"1"}}, false).Code)
	require.Equal(t, http.StatusSeeOther, x.post(t, "/legacy-group/delete", url.Values{"version": {"2"}}, false).Code)
	got := x.stored(t)
	require.NotNil(t, got.Rules.Items)
	require.Empty(t, got.Rules.Items)
	assert.Equal(t, device.OutcomeNone, got.Rules.Match(device.MatchInput{Body: "hi", SenderPhone: "6281111222333"}).Outcome)
	assert.Contains(t, x.do(t, http.MethodGet, x.path(""), "", false).Body.String(), "rules.empty_body")
}

func TestRules_UseInARulePrefillsTheSender(t *testing.T) {
	x := newRulesFixture(t)
	body := x.do(t, http.MethodGet, x.path("/fresh?sender=phone%3A6281111222333"), "", false).Body.String()
	assert.Contains(t, body, `name="sender" value="phone:6281111222333"`)
}

func TestRules_PickerSearchesGroupsAndContacts(t *testing.T) {
	x := newRulesFixture(t)
	groups := x.do(t, http.MethodGet, x.path("/a/pick?field=chat&q=tok"), "", true).Body.String()
	assert.Contains(t, groups, tokoJID)
	assert.NotContains(t, groups, sariJID, "the chat picker lists groups only")
	people := x.do(t, http.MethodGet, x.path("/a/pick?field=sender&q=sari"), "", true).Body.String()
	assert.Contains(t, people, "phone:6281111222333")
	typed := x.do(t, http.MethodGet, x.path("/a/pick?field=sender&q=0812+3456+7890"), "", true).Body.String()
	assert.NotContains(t, typed, "phone:0812", "a leading 0 is not offered as a phone value")
}

func ruleIDs(items []device.Rule) []string {
	out := make([]string, 0, len(items))
	for _, r := range items {
		out = append(out, r.ID)
	}
	return out
}

type rulesWriteRoute struct {
	name   string
	suffix string
	form   url.Values
}

func rulesWriteRoutes() []rulesWriteRoute {
	return []rulesWriteRoute{
		{"save", "/legacy-dm", tokoAndSari(1)},
		{"toggle", "/legacy-dm/toggle", url.Values{"version": {"1"}}},
		{"move", "/legacy-group/move", url.Values{"version": {"1"}, "to": {"up"}}},
		{"delete", "/legacy-dm/delete", url.Values{"version": {"1"}}},
	}
}

// SECURITY: a member is refused every rules write, by hand-made POST too, and the rules stay as they were.
func TestRules_WritesNeedAManager(t *testing.T) {
	for _, rt := range rulesWriteRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			x := newRulesFixture(t)
			member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)
			before := x.stored(t)
			rec := x.doAsPrincipal(t, http.MethodPost, x.path(rt.suffix), rt.form.Encode(), member)
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			assert.Contains(t, mainOf(t, rec.Body.String()), managersOnlyKey)
			after := x.stored(t)
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.Rules, after.Rules)

			admin := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleAdmin)
			rec = x.doAsPrincipal(t, http.MethodPost, x.path(rt.suffix), rt.form.Encode(), admin)
			require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		})
	}
}

// SECURITY: a member reads the rules and can run the test box, but gets no write controls.
func TestRules_MembersReadAndTestOnly(t *testing.T) {
	x := newRulesFixture(t)
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)
	list := x.doAsPrincipal(t, http.MethodGet, x.path(""), "", member).Body.String()
	assert.NotContains(t, list, "/toggle")
	assert.NotContains(t, list, "rules.create")
	page := x.doAsPrincipal(t, http.MethodGet, x.path("/legacy-dm"), "", member).Body.String()
	assert.NotContains(t, page, "data-rule-save")
	rec := x.doAsPrincipal(t, http.MethodPost, x.path("/legacy-dm/test"), url.Values{"test_chat": {x.sariDM.PublicID}, "groups": {"none"}}.Encode(), member)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "rules.test.sent", "a member's test runs the saved rule, not the posted form")
}

func (x *deviceFixture) doAsPrincipal(t *testing.T, method, target, body string, p session.Principal) *httptest.ResponseRecorder {
	t.Helper()
	req := x.authedRequest(t, method, target, body, p)
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, req)
	return rec
}

func testResultText(body string) string {
	i := strings.Index(body, `id="rule-test-result"`)
	if i < 0 {
		return ""
	}
	return body[i:]
}

func TestRules_BuilderShowsItsThreeTips(t *testing.T) {
	x := newRulesFixture(t)
	f := tokoAndSari(1)
	f.Set("groups", "these")
	require.Equal(t, http.StatusSeeOther, x.post(t, "/toko", f, false).Code)
	body := x.do(t, http.MethodGet, x.path("/toko"), "", false).Body.String()
	for _, id := range []string{"rules-where", "rules-groups", "rules-test"} {
		assert.Contains(t, body, `data-tip="`+id+`"`)
		assert.Contains(t, body, `data-tour="`+id+`"`, "the tip's anchor is on the page")
	}
	blank := x.do(t, http.MethodGet, x.path("/fresh"), "", false).Body.String()
	assert.NotContains(t, blank, `data-tip="rules-groups"`, "the groups tip waits until These groups is picked")
}

func TestRules_PairFromALegacyOrUnsuffixableIDMintsAFreshBase(t *testing.T) {
	long := strings.Repeat("a", 62)
	for name, tc := range map[string]struct {
		id   string
		form url.Values
	}{
		"legacy-group plus DMs":     {"legacy-group", url.Values{"groups": {"all"}, "mention": {"1"}, "dm": {"1"}}},
		"legacy-dm plus all groups": {"legacy-dm", url.Values{"groups": {"all"}, "dm": {"1"}}},
		"62-character id":           {long, url.Values{"groups": {"all"}, "dm": {"1"}}},
		"id ending in -dm":          {"orders-dm", url.Values{"groups": {"all"}, "dm": {"1"}}},
	} {
		t.Run(name, func(t *testing.T) {
			x := newRulesFixture(t)
			f := tc.form
			f.Set("version", "1")
			f.Set("who", "everyone")
			f.Set("action", "match")
			rec := x.post(t, "/"+tc.id, f, false)
			require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
			ids := ruleIDs(x.stored(t).Rules.Items)
			assert.NotContains(t, ids, tc.id, "the old rule is replaced")
			var base string
			for _, id := range ids {
				if b, ok := strings.CutSuffix(id, "-dm"); ok && slices.Contains(ids, b) {
					base = b
				}
			}
			require.NotEmpty(t, base, "a pair was saved: %v", ids)
			assert.LessOrEqual(t, len(base), 61)
			assert.False(t, strings.HasPrefix(base, "legacy-"))
		})
	}
}

func TestRules_TestBoxTakesThePhoneFromTheJIDLikeIngest(t *testing.T) {
	x := newRulesFixture(t)
	c, err := chat.New(x.org, x.project, x.dev.ID, fakes.ChatPublicID(), "628555000111@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	x.chatStore.Seed(c)
	f := url.Values{"dm": {"1"}, "groups": {"none"}, "who": {"only"}, "sender": {"phone:628555000111"}, "action": {"match"}, "test_chat": {c.PublicID}}
	rec := x.post(t, "/dm/test", f, true)
	require.Equal(t, http.StatusOK, rec.Code)
	got := testResultText(rec.Body.String())
	assert.Contains(t, got, "rules.test.shadowed_sent", "the draft holds (the default DM rule decides first): no contact row, yet ingest reads the phone from the JID")
	assert.NotContains(t, got, "rules.test.not_only")
}

func TestRules_DeleteDialogSitsOutsideTheRowMenu(t *testing.T) {
	x := newRulesFixture(t)
	body := x.do(t, http.MethodGet, x.path(""), "", false).Body.String()
	row := body[strings.Index(body, `id="rule-legacy-dm"`):strings.Index(body, `id="rule-legacy-group"`)]
	menuEnd := strings.Index(row, "</details>")
	dialog := strings.Index(row, `<dialog id="rule-delete-legacy-dm"`)
	require.Positive(t, menuEnd)
	require.Greater(t, dialog, menuEnd, "the dialog is not inside the details panel")
	assert.Contains(t, row[:menuEnd], `data-dialog-open="rule-delete-legacy-dm"`)
}

func TestRules_PickerNamesAContactWithNoNameOrPhone(t *testing.T) {
	x := newRulesFixture(t)
	people := x.do(t, http.MethodGet, x.path("/a/pick?field=sender"), "", true).Body.String()
	assert.Contains(t, people, `data-rule-pick-value="55555@lid" data-rule-pick-label="rules.hidden_number"`)
	assert.Contains(t, people, "max-lg:min-h-11", "picker rows are 44px on phones")
}

func TestRules_AnEmptyNameIsNamedFromTheSentence(t *testing.T) {
	x := newRulesFixture(t)
	f := tokoAndSari(1)
	f.Set("name", " ")
	require.Equal(t, http.StatusSeeOther, x.post(t, "/toko", f, false).Code)
	items := x.stored(t).Rules.Items
	assert.Equal(t, "rules.sentence.full", items[len(items)-1].Name, "the translator in tests returns keys, so the key is the sentence")
}

// SECURITY: a rule id that is not a valid id (a decoded "../delete") is refused before anything renders, so no form can aim Save at another device action.
func TestRules_AnInvalidRuleIDIsNotFoundOnEveryRoute(t *testing.T) {
	routes := []struct{ method, suffix string }{
		{http.MethodGet, ""}, {http.MethodPost, ""}, {http.MethodPost, "/preview"}, {http.MethodPost, "/test"},
		{http.MethodPost, "/toggle"}, {http.MethodPost, "/move"}, {http.MethodPost, "/delete"}, {http.MethodGet, "/pick?field=chat"},
	}
	for _, bad := range []string{"..%2Fdelete", "..%2Funlink", "a%20b", "legacy-x"} {
		for _, rt := range routes {
			t.Run(bad+" "+rt.method+rt.suffix, func(t *testing.T) {
				x := newRulesFixture(t)
				before := x.stored(t)
				rec := x.do(t, rt.method, x.path("/"+bad+rt.suffix), tokoAndSari(1).Encode(), rt.suffix != "")
				require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
				assert.NotContains(t, rec.Body.String(), `id="rule-form"`)
				assert.NotContains(t, rec.Body.String(), "/delete\"")
				after := x.stored(t)
				require.Equal(t, before.Version, after.Version)
			})
		}
	}
}

func TestRules_TheTextRowTemplateMarksOnlyTheRowIndex(t *testing.T) {
	x := newRulesFixture(t)
	body := x.do(t, http.MethodGet, x.path("/legacy-dm"), "", false).Body.String()
	tpl := body[strings.Index(body, `<template id="rule-text-template">`):]
	tpl = tpl[:strings.Index(tpl, "</template>")]
	assert.Contains(t, tpl, `name="text.__i__.op"`)
	assert.Contains(t, tpl, `data-rule-text="__i__"`)
	assert.NotContains(t, tpl, "text.-1", "the old -1 index is gone, so the client swap cannot touch flex-1 or SVG paths")
	assert.NotContains(t, tpl, "rules.didnt_match", "a fresh row never carries a Didn't match badge")
}

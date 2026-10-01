package ui

import (
	"strings"
	"testing"
)

func TestMessageSendModel_SummarisesMediaAndStatus(t *testing.T) {
	vm := newJSVM(t)
	got := jsString(t, vm, `JSON.stringify(renderTool("message_send", `+fixtureJSON(t, "message_send.json")+`).model)`)
	for _, want := range []string{`"status":"queued"`, `"summary":"Image · invoice.png · Invoice for September"`, `"day":"2026-09-28"`, `"error":""`} {
		if !strings.Contains(got, want) {
			t.Errorf("model missing %s: %s", want, got)
		}
	}
	failed := jsString(t, vm, `renderTool("message_send", {"message":{"status":"failed","error":"not on WhatsApp"}}).model.error`)
	if failed != "not on WhatsApp" {
		t.Errorf("failed error = %q", failed)
	}
	actions := jsString(t, vm, `Object.keys(renderTool("message_send", `+fixtureJSON(t, "message_send.json")+`).actions).length + ""`)
	if actions != "0" {
		t.Errorf("message_send offers %s actions, want none", actions)
	}
}

func TestChatListModel_KPIsAndOneReplyPerOpenChat(t *testing.T) {
	vm := newJSVM(t)
	fx := fixtureJSON(t, "chat_list.json")
	kpis := jsString(t, vm, `renderTool("chat_list", `+fx+`).model.kpis.map(function (k) { return k.label + "=" + k.value; }).join(",")`)
	if kpis != "Chats=3,Groups=2,Unread=2" {
		t.Fatalf("kpis = %q", kpis)
	}
	actions := jsString(t, vm, `JSON.stringify(renderTool("chat_list", `+fx+`).actions)`)
	if strings.Contains(actions, "cht_Wx2Yn6Pq8Rt1Uv4c") {
		t.Errorf("an archived chat offers a reply: %s", actions)
	}
	if n := jsString(t, vm, `Object.keys(renderTool("chat_list", `+fx+`).actions).length + ""`); n != "2" {
		t.Errorf("reply actions = %s, want 2", n)
	}
	for _, want := range []string{`"tool":"message_send"`, `"to":"628111222333@s.whatsapp.net"`, `"deviceId":"dev_SalesDevice00001"`} {
		if !strings.Contains(actions, want) {
			t.Errorf("reply action missing %s: %s", want, actions)
		}
	}
	more := jsString(t, vm, `renderTool("chat_list", `+fx+`).model.more + ""`)
	if more != "true" {
		t.Errorf("more = %q", more)
	}
}

func TestChatListModel_NameFallsBackToTheJIDUser(t *testing.T) {
	vm := newJSVM(t)
	got := jsString(t, vm, `renderTool("chat_list", {"chats":[{"id":"c","jid":"628999@s.whatsapp.net"}]}).model.rows[0].name`)
	if got != "628999" {
		t.Errorf("name = %q", got)
	}
}

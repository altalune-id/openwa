package ui

import (
	"strings"
	"testing"
)

func TestRenderMessageSend(t *testing.T) {
	rep := renderInBrowser(t, "message_send", "openwa-message-send", fixtureJSON(t, "message_send.json"))
	if !rep.Scoped {
		t.Error("the view rendered outside a shadow root")
	}
	for _, want := range []string{"queued", "invoice.png", "msg_Ab1Cd2Ef3Gh4Ij5k"} {
		if !strings.Contains(rep.Text, want) {
			t.Errorf("rendered message is missing %q:\n%s", want, rep.Text)
		}
	}
	if rep.Buttons != 0 {
		t.Errorf("rendered %d buttons, want none", rep.Buttons)
	}
}

func TestRenderChatList(t *testing.T) {
	rep := renderInBrowser(t, "chat_list", "openwa-chat-list", fixtureJSON(t, "chat_list.json"))
	for _, want := range []string{"Budi", "Ops", "Terima kasih!", "nextCursor"} {
		if !strings.Contains(rep.Text, want) {
			t.Errorf("rendered list is missing %q:\n%s", want, rep.Text)
		}
	}
	if rep.Buttons != 2 {
		t.Errorf("rendered %d Send buttons, want 2 (the archived chat has none)", rep.Buttons)
	}
}

func TestRenderChatListReplyRefusesAnEmptySubmit(t *testing.T) {
	fx := fixtureJSON(t, "chat_list.json")
	if rep := submitInBrowser(t, "chat_list", "openwa-chat-list", fx, ""); rep.Actions != 0 {
		t.Errorf("an empty reply dispatched %d actions, want none: reportValidity must stop it", rep.Actions)
	}
	if rep := submitInBrowser(t, "chat_list", "openwa-chat-list", fx, "halo"); rep.Actions != 1 {
		t.Errorf("a reply with text dispatched %d actions, want 1", rep.Actions)
	}
}

func TestRenderChatListEscapesAHostilePreview(t *testing.T) {
	rep := renderInBrowser(t, "chat_list", "openwa-chat-list", `{"chats":[{"id":"c","jid":"1@s.whatsapp.net","name":"x","lastMessagePreview":"<img src=x onerror=alert(1)>"}]}`)
	if rep.Images != 0 {
		t.Errorf("a preview became markup: %d images", rep.Images)
	}
	if !strings.Contains(rep.Text, "<img src=x") {
		t.Errorf("the preview text was not rendered literally:\n%s", rep.Text)
	}
}

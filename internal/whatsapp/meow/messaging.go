package meow

import (
	"context"
	"os"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"altalune.id/openwa/internal/whatsapp"
)

var _ whatsapp.MessagingSession = (*session)(nil)

func (s *session) client() (*whatsmeow.Client, error) {
	if s.isClosed() || s.cli == nil {
		return nil, &whatsapp.NotConnectedError{ID: s.deviceID.String()}
	}
	raw := s.cli.Raw()
	if raw == nil {
		return nil, &whatsapp.NotConnectedError{ID: s.deviceID.String()}
	}
	return raw, nil
}

func ownJID(cli *whatsmeow.Client) types.JID {
	if cli.Store == nil || cli.Store.ID == nil {
		return types.EmptyJID
	}
	return *cli.Store.ID
}

// Send uploads media when present, builds the typed message and sends it under out.WAID.
func (s *session) Send(ctx context.Context, out whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
	cli, err := s.client()
	if err != nil {
		return whatsapp.SendResult{}, err
	}
	to, err := types.ParseJID(out.To)
	if err != nil {
		return whatsapp.SendResult{}, &whatsapp.InvalidJIDError{Raw: out.To}
	}
	var up *whatsmeow.UploadResponse
	if out.Media != nil {
		mt, ok := mediaTypeFor(out.Kind)
		if !ok {
			return whatsapp.SendResult{}, &whatsapp.UnsupportedError{Feature: "media kind " + out.Kind}
		}
		resp, uploadErr := cli.Upload(ctx, out.Media.Bytes, mt)
		if uploadErr != nil {
			return whatsapp.SendResult{}, mapError("upload", s.deviceID.String(), uploadErr)
		}
		up = &resp
	}
	msg, err := buildMessage(out, to.ToNonAD(), up, ownJID(cli), cli)
	if err != nil {
		return whatsapp.SendResult{}, err
	}
	resp, err := cli.SendMessage(ctx, to.ToNonAD(), msg, whatsmeow.SendRequestExtra{ID: out.WAID})
	if err != nil {
		return whatsapp.SendResult{}, mapError("send", s.deviceID.String(), err)
	}
	res := whatsapp.SendResult{At: resp.Timestamp, ChatJID: to.ToNonAD().String()}
	if up != nil {
		res.Media = &whatsapp.MediaKeys{URL: up.URL, DirectPath: up.DirectPath, MediaKey: up.MediaKey,
			FileSHA256: up.FileSHA256, FileEncSHA256: up.FileEncSHA256, Length: up.FileLength}
	}
	return res, nil
}

// MarkRead sends read receipts, or played receipts for voice notes.
func (s *session) MarkRead(ctx context.Context, chat, sender string, waIDs []string, played bool) error {
	cli, err := s.client()
	if err != nil {
		return err
	}
	chatJID, err := types.ParseJID(chat)
	if err != nil {
		return &whatsapp.InvalidJIDError{Raw: chat}
	}
	senderJID := types.EmptyJID
	if sender != "" {
		if senderJID, err = types.ParseJID(sender); err != nil {
			return &whatsapp.InvalidJIDError{Raw: sender}
		}
	}
	var extra []types.ReceiptType
	if played {
		extra = append(extra, types.ReceiptTypePlayed)
	}
	if err := cli.MarkRead(ctx, waIDs, time.Now(), chatJID, senderJID, extra...); err != nil {
		return mapError("mark_read", s.deviceID.String(), err)
	}
	return nil
}

// SendTyping shows or clears the composing indicator.
func (s *session) SendTyping(ctx context.Context, chat string, on bool) error {
	cli, err := s.client()
	if err != nil {
		return err
	}
	jid, err := types.ParseJID(chat)
	if err != nil {
		return &whatsapp.InvalidJIDError{Raw: chat}
	}
	state := types.ChatPresencePaused
	if on {
		state = types.ChatPresenceComposing
	}
	return cli.SendChatPresence(ctx, jid, state, types.ChatPresenceMediaText)
}

// FetchMedia downloads an attachment into an unlinked temp file.
func (s *session) FetchMedia(ctx context.Context, keys whatsapp.MediaKeys, kind string) (*os.File, error) {
	cli, err := s.client()
	if err != nil {
		return nil, err
	}
	return fetchToTemp(ctx, cli, keys, kind, s.deviceID.String())
}

// IsOnWhatsApp maps each registered phone (digits) to its JID.
func (s *session) IsOnWhatsApp(ctx context.Context, phones []string) (map[string]string, error) {
	cli, err := s.client()
	if err != nil {
		return nil, err
	}
	query := make([]string, 0, len(phones))
	for _, p := range phones {
		query = append(query, "+"+strings.TrimPrefix(p, "+"))
	}
	resp, err := cli.IsOnWhatsApp(ctx, query)
	if err != nil {
		return nil, mapError("is_on_whatsapp", s.deviceID.String(), err)
	}
	out := make(map[string]string, len(resp))
	for _, r := range resp {
		if r.IsIn {
			out[strings.TrimPrefix(r.Query, "+")] = r.JID.ToNonAD().String()
		}
	}
	return out, nil
}

// GroupList returns the joined groups.
func (s *session) GroupList(ctx context.Context) ([]whatsapp.GroupInfo, error) {
	cli, err := s.client()
	if err != nil {
		return nil, err
	}
	groups, err := cli.GetJoinedGroups(ctx)
	if err != nil {
		return nil, mapError("group_list", s.deviceID.String(), err)
	}
	out := make([]whatsapp.GroupInfo, 0, len(groups))
	for _, g := range groups {
		out = append(out, toGroupInfo(g))
	}
	return out, nil
}

// GroupInfo returns one group's details.
func (s *session) GroupInfo(ctx context.Context, jid string) (whatsapp.GroupInfo, error) {
	cli, err := s.client()
	if err != nil {
		return whatsapp.GroupInfo{}, err
	}
	gj, err := types.ParseJID(jid)
	if err != nil {
		return whatsapp.GroupInfo{}, &whatsapp.InvalidJIDError{Raw: jid}
	}
	g, err := cli.GetGroupInfo(ctx, gj)
	if err != nil {
		return whatsapp.GroupInfo{}, mapError("group_info", s.deviceID.String(), err)
	}
	return toGroupInfo(g), nil
}

// GroupJoin joins a group by invite link.
func (s *session) GroupJoin(ctx context.Context, link string) (string, error) {
	cli, err := s.client()
	if err != nil {
		return "", err
	}
	code, err := inviteCode(link)
	if err != nil {
		return "", err
	}
	jid, err := cli.JoinGroupWithLink(ctx, code)
	if err != nil {
		return "", mapError("group_join", s.deviceID.String(), err)
	}
	return jid.String(), nil
}

// GroupLeave leaves a group.
func (s *session) GroupLeave(ctx context.Context, jid string) error {
	cli, err := s.client()
	if err != nil {
		return err
	}
	gj, err := types.ParseJID(jid)
	if err != nil {
		return &whatsapp.InvalidJIDError{Raw: jid}
	}
	if err := cli.LeaveGroup(ctx, gj); err != nil {
		return mapError("group_leave", s.deviceID.String(), err)
	}
	return nil
}

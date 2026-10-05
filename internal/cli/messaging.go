package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "altalune.id/openwa/gen/go/chat/v1"
	contactv1 "altalune.id/openwa/gen/go/contact/v1"
	messagev1 "altalune.id/openwa/gen/go/message/v1"
	"altalune.id/openwa/internal/cli/render"
)

type pageFlags struct {
	cursor string
	limit  int32
	all    bool
}

func addDeviceFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().String("device", "", "device name or id; empty uses the project's only device")
}

func addPageFlags(cmd *cobra.Command, p *pageFlags) {
	cmd.Flags().StringVar(&p.cursor, "cursor", "", "next_cursor of a previous page")
	cmd.Flags().Int32Var(&p.limit, "limit", 0, "page size, 1 to 200 (server default 50)")
	cmd.Flags().BoolVar(&p.all, "all", false, "follow next_cursor to the last page")
}

func (p pageFlags) check() error {
	if p.all && p.cursor != "" {
		return errors.New("list: --all walks every page itself; drop --cursor")
	}
	return nil
}

type page struct {
	items []map[string]any
	rows  [][]string
	next  string
}

func renderPage(cmd *cobra.Command, p pageFlags, cols []string, fetch func(cursor string) (page, error)) error {
	if err := p.check(); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	format := render.Detect(cmd)
	var all page
	seen := map[string]bool{}
	cursor := p.cursor
	for {
		pg, err := fetch(cursor)
		if err != nil {
			return err
		}
		if format == render.FormatNDJSON {
			if err := render.NDJSON(out, slices.Values(anySlice(pg.items))); err != nil {
				return err
			}
		} else {
			all.items = append(all.items, pg.items...)
			all.rows = append(all.rows, pg.rows...)
		}
		all.next = pg.next
		if !p.all || pg.next == "" {
			break
		}
		seen[cursor] = true
		if seen[pg.next] {
			return fmt.Errorf("list: the server returned cursor %q again; stopping instead of looping", pg.next)
		}
		cursor = pg.next
	}
	switch format {
	case render.FormatNDJSON:
		return nil
	case render.FormatJSON:
		return render.JSON(out, map[string]any{"items": all.items, "next_cursor": all.next})
	}
	if err := render.Table(out, cols, all.rows); err != nil {
		return err
	}
	if all.next != "" {
		_, err := fmt.Fprintf(cmd.ErrOrStderr(), "more: --cursor %s\n", all.next)
		return err
	}
	return nil
}

func anySlice(items []map[string]any) []any {
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = it
	}
	return out
}

// NOTE: ndjson prints a single record as json.
func one(cmd *cobra.Command, v map[string]any, text string) error {
	if render.Detect(cmd) != render.FormatText {
		return render.JSON(cmd.OutOrStdout(), v)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), text)
	return err
}

// NOTE: reads path whole; the server's whatsapp.mediaMaxBytes decides whether it is too big.
func readMediaFile(path string) (data []byte, mimeType string, err error) {
	data, err = os.ReadFile(path) //nolint:gosec // G304: the operator names the file to send.
	if err != nil {
		return nil, "", fmt.Errorf("send: %w", err)
	}
	mimeType = http.DetectContentType(data)
	return data, mimeType, nil
}

func ts(t *timestamppb.Timestamp) string {
	if t == nil {
		return ""
	}
	return rfc3339UTC(t.AsTime())
}

func messageMap(m *messagev1.Message) map[string]any {
	out := map[string]any{
		"id": m.GetId(), "device_id": m.GetDeviceId(), "chat_id": m.GetChatId(), "direction": m.GetDirection(),
		"wa_id": m.GetWaId(), "type": m.GetType(), "status": m.GetStatus(), "body": m.GetBody(),
		"sender_phone": m.GetSenderPhone(), "sender_name": m.GetSenderName(), "from_me": m.GetFromMe(),
		"error": m.GetError(),
	}
	if m.GetTimestamp() != nil {
		out["timestamp"] = ts(m.GetTimestamp())
	}
	if md := m.GetMedia(); md != nil {
		out["media"] = map[string]any{"mime": md.GetMime(), "size": md.GetSize(), "filename": md.GetFilename(), "url": md.GetUrl()}
	}
	if loc := m.GetLocation(); loc != nil {
		out["location"] = map[string]any{"lat": loc.GetLat(), "lng": loc.GetLng(), "name": loc.GetName(), "address": loc.GetAddress()}
	}
	return out
}

func messageRow(m *messagev1.Message) []string {
	who := "me"
	if m.GetDirection() == "in" {
		who = phoneText(m.GetSenderPhone())
		if n := m.GetSenderName(); n != "" {
			who = n
		}
	}
	body := m.GetBody()
	if body == "" {
		body = "[" + m.GetType() + "]"
	}
	when := ""
	if m.GetTimestamp() != nil {
		when = tableDateTime(m.GetTimestamp().AsTime())
	}
	return []string{when, who, body, m.GetStatus(), m.GetId()}
}

func chatMap(c *chatv1.Chat) map[string]any {
	out := map[string]any{
		"id": c.GetId(), "device_id": c.GetDeviceId(), "jid": c.GetJid(), "kind": c.GetKind(), "name": c.GetName(),
		"last_message_preview": c.GetLastMessagePreview(), "unread_count": c.GetUnreadCount(), "archived": c.GetArchived(),
	}
	if c.GetLastMessageAt() != nil {
		out["last_message_at"] = ts(c.GetLastMessageAt())
	}
	return out
}

func contactMap(c *contactv1.Contact) map[string]any {
	return map[string]any{
		"device_id": c.GetDeviceId(), "jid": c.GetJid(), "phone": c.GetPhone(), "name": c.GetName(),
		"push_name": c.GetPushName(), "business_name": c.GetBusinessName(), "display_name": c.GetDisplayName(),
		"updated_at": ts(c.GetUpdatedAt()),
	}
}

func groupMap(g *chatv1.Group) map[string]any {
	return map[string]any{
		"jid": g.GetJid(), "name": g.GetName(), "topic": g.GetTopic(), "participants": g.GetParticipants(),
		"announce": g.GetAnnounce(), "locked": g.GetLocked(), "invite_link": g.GetInviteLink(),
		"chat_id": g.GetChatId(), "joined": g.GetJoined(),
	}
}

func itoa(n int32) string { return strconv.FormatInt(int64(n), 10) }

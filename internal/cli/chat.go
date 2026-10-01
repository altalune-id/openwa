package cli

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	chatv1 "altalune.id/openwa/gen/go/chat/v1"
	messagev1 "altalune.id/openwa/gen/go/message/v1"
	"altalune.id/openwa/internal/cli/render"
)

func newChatCmd(bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "chat",
		Short:   "List and inspect WhatsApp chats in the active project",
		GroupID: "domain",
	}
	addDeviceFlag(cmd)
	cmd.AddCommand(newChatListCmd(bootClient), newChatShowCmd(bootClient))
	return cmd
}

func newChatListCmd(bootClient ClientBootFn) *cobra.Command {
	var p pageFlags
	var kind, q string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List chats, most recent activity first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if kind != "" && kind != "dm" && kind != "group" {
				return fmt.Errorf("chat list: --kind is dm or group, got %q", kind)
			}
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			deviceID, err := deviceIDFrom(cmd, conn, false)
			if err != nil {
				return err
			}
			return renderPage(cmd, p, []string{"NAME", "KIND", "UNREAD", "LAST", "PREVIEW", "ID"}, func(cursor string) (page, error) {
				resp, err := conn.Chat.List(cmd.Context(), connect.NewRequest(&chatv1.ListRequest{DeviceId: deviceID, Kind: kind, Q: q, Cursor: cursor, Limit: p.limit}))
				if err != nil {
					return page{}, deviceRPCError(err)
				}
				var pg page
				for _, c := range resp.Msg.GetChats() {
					last := ""
					if c.GetLastMessageAt() != nil {
						last = c.GetLastMessageAt().AsTime().Local().Format("2006-01-02 15:04")
					}
					pg.items = append(pg.items, chatMap(c))
					pg.rows = append(pg.rows, []string{c.GetName(), c.GetKind(), itoa(c.GetUnreadCount()), last, c.GetLastMessagePreview(), c.GetId()})
				}
				pg.next = resp.Msg.GetNextCursor()
				return pg, nil
			})
		},
	}
	addPageFlags(cmd, &p)
	cmd.Flags().StringVar(&kind, "kind", "", "dm or group")
	cmd.Flags().StringVar(&q, "q", "", "case-insensitive name prefix")
	return cmd
}

func newChatShowCmd(bootClient ClientBootFn) *cobra.Command {
	var limit int32
	cmd := &cobra.Command{
		Use:   "show <chat-id>",
		Short: "Show a chat and its latest messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			got, err := conn.Chat.Get(cmd.Context(), connect.NewRequest(&chatv1.GetRequest{ChatId: args[0]}))
			if err != nil {
				return deviceRPCError(err)
			}
			msgs, err := conn.Message.List(cmd.Context(), connect.NewRequest(&messagev1.ListRequest{ChatId: args[0], Limit: limit}))
			if err != nil {
				return deviceRPCError(err)
			}
			c := got.Msg.GetChat()
			if render.Detect(cmd) != render.FormatText {
				items := make([]map[string]any, 0, len(msgs.Msg.GetMessages()))
				for _, m := range msgs.Msg.GetMessages() {
					items = append(items, messageMap(m))
				}
				out := chatMap(c)
				out["messages"] = items
				return one(cmd, out, "")
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "id:     %s\nname:   %s\njid:    %s\nkind:   %s\nunread: %d\n\n", c.GetId(), c.GetName(), c.GetJid(), c.GetKind(), c.GetUnreadCount()); err != nil {
				return err
			}
			rows := make([][]string, 0, len(msgs.Msg.GetMessages()))
			for _, m := range msgs.Msg.GetMessages() {
				rows = append(rows, messageRow(m))
			}
			return render.Table(cmd.OutOrStdout(), []string{"WHEN", "FROM", "BODY", "STATUS", "ID"}, rows)
		},
	}
	cmd.Flags().Int32Var(&limit, "limit", 20, "how many recent messages to show")
	return cmd
}

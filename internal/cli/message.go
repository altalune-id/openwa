package cli

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	messagev1 "altalune.id/openwa/gen/go/message/v1"
)

func newMessageCmd(bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "message",
		Short:   "List WhatsApp messages in the active project",
		GroupID: "domain",
	}
	addDeviceFlag(cmd)
	cmd.AddCommand(newMessageListCmd(bootClient))
	return cmd
}

func newMessageListCmd(bootClient ClientBootFn) *cobra.Command {
	var p pageFlags
	var chatID, since string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List a chat's (--chat) or a device's (--device) messages, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			deviceID, err := deviceIDFrom(cmd, conn, false)
			if err != nil {
				return err
			}
			if chatID == "" && deviceID == "" {
				return errors.New("message list: pass --chat or --device")
			}
			return renderPage(cmd, p, []string{"WHEN", "FROM", "BODY", "STATUS", "ID"}, func(cursor string) (page, error) {
				resp, err := conn.Message.List(cmd.Context(), connect.NewRequest(&messagev1.ListRequest{DeviceId: deviceID, ChatId: chatID, SinceId: since, Cursor: cursor, Limit: p.limit}))
				if err != nil {
					return page{}, deviceRPCError(err)
				}
				var pg page
				for _, m := range resp.Msg.GetMessages() {
					pg.items = append(pg.items, messageMap(m))
					pg.rows = append(pg.rows, messageRow(m))
				}
				pg.next = resp.Msg.GetNextCursor()
				return pg, nil
			})
		},
	}
	addPageFlags(cmd, &p)
	cmd.Flags().StringVar(&chatID, "chat", "", "chat id")
	cmd.Flags().StringVar(&since, "since", "", "message id; list newer messages, oldest first")
	return cmd
}

package cli

import (
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	chatv1 "altalune.id/openwa/gen/go/chat/v1"
	"altalune.id/openwa/internal/cli/render"
)

func newGroupCmd(bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "group",
		Short:   "List, join and leave WhatsApp groups",
		GroupID: "domain",
	}
	addDeviceFlag(cmd)
	cmd.AddCommand(newGroupListCmd(bootClient), newGroupJoinCmd(bootClient), newGroupLeaveCmd(bootClient))
	return cmd
}

func newGroupListCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the device's groups, then stored groups it has left",
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
			resp, err := conn.Chat.ListGroups(cmd.Context(), connect.NewRequest(&chatv1.ListGroupsRequest{DeviceId: deviceID}))
			if err != nil {
				return deviceRPCError(err)
			}
			groups := resp.Msg.GetGroups()
			if render.Detect(cmd) != render.FormatText {
				return renderPage(cmd, pageFlags{}, nil, func(string) (page, error) {
					var pg page
					for _, g := range groups {
						pg.items = append(pg.items, groupMap(g))
					}
					return pg, nil
				})
			}
			rows := make([][]string, 0, len(groups))
			for _, g := range groups {
				joined := "yes"
				if !g.GetJoined() {
					joined = "left"
				}
				rows = append(rows, []string{g.GetName(), itoa(g.GetParticipants()), joined, g.GetJid(), g.GetChatId()})
			}
			return render.Table(cmd.OutOrStdout(), []string{"NAME", "MEMBERS", "JOINED", "JID", "CHAT ID"}, rows)
		},
	}
}

func newGroupJoinCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "join <invite-link>",
		Short: "Join a group by its invite link (https://chat.whatsapp.com/...)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			deviceID, err := deviceIDFrom(cmd, conn, false)
			if err != nil {
				return err
			}
			resp, err := conn.Chat.JoinGroup(cmd.Context(), connect.NewRequest(&chatv1.JoinGroupRequest{DeviceId: deviceID, InviteLink: args[0]}))
			if err != nil {
				return deviceRPCError(err)
			}
			c := resp.Msg.GetChat()
			return one(cmd, chatMap(c), fmt.Sprintf("Joined group %s (%s)", c.GetName(), c.GetId()))
		},
	}
}

func newGroupLeaveCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "leave <chat-id>",
		Short: "Leave a group; its chat is archived and kept",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			resp, err := conn.Chat.LeaveGroup(cmd.Context(), connect.NewRequest(&chatv1.LeaveGroupRequest{ChatId: args[0]}))
			if err != nil {
				return deviceRPCError(err)
			}
			c := resp.Msg.GetChat()
			return one(cmd, chatMap(c), "Left group "+c.GetJid())
		},
	}
}

package cli

import (
	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	contactv1 "altalune.id/openwa/gen/go/contact/v1"
)

func newContactCmd(bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "contact",
		Short:   "List WhatsApp contacts the project's devices know",
		GroupID: "domain",
	}
	addDeviceFlag(cmd)
	cmd.AddCommand(newContactListCmd(bootClient))
	return cmd
}

func newContactListCmd(bootClient ClientBootFn) *cobra.Command {
	var p pageFlags
	var q string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List contacts, most recently updated first",
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
			return renderPage(cmd, p, []string{"NAME", "PHONE", "JID", "DEVICE"}, func(cursor string) (page, error) {
				resp, err := conn.Contact.List(cmd.Context(), connect.NewRequest(&contactv1.ListRequest{DeviceId: deviceID, Q: q, Cursor: cursor, Limit: p.limit}))
				if err != nil {
					return page{}, deviceRPCError(err)
				}
				var pg page
				for _, c := range resp.Msg.GetContacts() {
					pg.items = append(pg.items, contactMap(c))
					pg.rows = append(pg.rows, []string{c.GetDisplayName(), phoneText(c.GetPhone()), c.GetJid(), c.GetDeviceId()})
				}
				pg.next = resp.Msg.GetNextCursor()
				return pg, nil
			})
		},
	}
	addPageFlags(cmd, &p)
	cmd.Flags().StringVar(&q, "q", "", "prefix on the address-book name")
	return cmd
}

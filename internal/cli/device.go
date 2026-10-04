package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/skip2/go-qrcode"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	devicev1 "altalune.id/openwa/gen/go/device/v1"
	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/cli/render"
	"altalune.id/openwa/internal/controlplane"
	"altalune.id/openwa/internal/platform/publicid"
)

const devicePollEvery = 5 * time.Second

// NOTE: mirrors device.PublicIDPrefix; the CLI is a Connect client and imports no domain module (R11).
const devicePrefix = "dev"

func newDeviceCmd(bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "device",
		Short:   "Manage WhatsApp devices in the active project",
		Long:    "Manage WhatsApp devices over the control plane (S2). The token's principal decides the org and project.",
		GroupID: "domain",
	}
	cmd.PersistentFlags().String("device", "", "device name or public id (dev_…); pair and logout default to the project's only device")
	cmd.AddCommand(
		newDeviceListCmd(bootClient),
		newDeviceGetCmd(bootClient),
		newDeviceCreateCmd(bootClient),
		newDevicePairCmd(bootClient),
		newDeviceLogoutCmd(bootClient),
		newDeviceDeleteCmd(bootClient),
	)
	for _, sub := range cmd.Commands() {
		run := sub.RunE
		sub.RunE = func(c *cobra.Command, args []string) error {
			return deviceRPCError(run(c, args))
		}
	}
	return cmd
}

// PairInterruptedError reports a pairing attempt stopped before the device linked.
type PairInterruptedError struct{}

func (*PairInterruptedError) Error() string {
	return "device pair: interrupted before the device linked"
}

// IsPairInterruptedError reports whether err's tree contains a *PairInterruptedError.
func IsPairInterruptedError(err error) bool {
	_, ok := errors.AsType[*PairInterruptedError](err)
	return ok
}

func deviceRPCError(err error) error {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return err
	}
	code := apperror.CodeUnexpectedError
	for _, d := range cerr.Details() {
		v, verr := d.Value()
		if verr != nil {
			continue
		}
		if ed, ok := v.(*apperrorv1.ErrorDetail); ok && ed.GetCode() != "" {
			code = ed.GetCode()
			break
		}
	}
	return apperror.New(code, cerr.Message(), codes.Code(cerr.Code())).WithCause(err)
}

func newDeviceListCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List devices with their state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			resp, err := conn.Device.ListDevices(cmd.Context(), connect.NewRequest(&devicev1.ListDevicesRequest{}))
			if err != nil {
				return err
			}
			devices := resp.Msg.GetDevices()
			if render.Detect(cmd) != render.FormatText {
				out := make([]map[string]any, 0, len(devices))
				for _, d := range devices {
					out = append(out, deviceMap(d))
				}
				return render.JSON(cmd.OutOrStdout(), out)
			}
			rows := make([][]string, 0, len(devices))
			for _, d := range devices {
				rows = append(rows, []string{d.GetName(), d.GetState(), phoneText(d.GetPhone()), d.GetId()})
			}
			return render.Table(cmd.OutOrStdout(), []string{"NAME", "STATE", "PHONE", "ID"}, rows)
		},
	}
}

func newDeviceGetCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show one device (--device is required)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			id, err := deviceIDFrom(cmd, conn, true)
			if err != nil {
				return err
			}
			resp, err := conn.Device.GetDevice(cmd.Context(), connect.NewRequest(&devicev1.GetDeviceRequest{DeviceId: id}))
			if err != nil {
				return err
			}
			return renderDevice(cmd, resp.Msg.GetDevice())
		},
	}
}

func newDeviceCreateCmd(bootClient ClientBootFn) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a device in the active project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			resp, err := conn.Device.CreateDevice(cmd.Context(), connect.NewRequest(&devicev1.CreateDeviceRequest{Name: name}))
			if err != nil {
				return err
			}
			d := resp.Msg.GetDevice()
			if render.Detect(cmd) != render.FormatText {
				return render.JSON(cmd.OutOrStdout(), deviceMap(d))
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Created device %s (%s)\n", d.GetName(), d.GetId())
			return err
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "device name, 1 to 64 characters (required)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func newDevicePairCmd(bootClient ClientBootFn) *cobra.Command {
	var phone string
	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair a device by QR code (or --phone for a pairing code) and wait until it connects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := runDevicePair(cmd, bootClient, phone)
			if err != nil && cmd.Context().Err() != nil {
				return &PairInterruptedError{}
			}
			return err
		},
	}
	cmd.Flags().StringVar(&phone, "phone", "", "pair with an 8-character code instead of a QR; the phone number in international format")
	return cmd
}

func runDevicePair(cmd *cobra.Command, bootClient ClientBootFn, phone string) error {
	conn, err := connFromCmd(cmd, bootClient)
	if err != nil {
		return err
	}
	id, err := deviceIDFrom(cmd, conn, false)
	if err != nil {
		return err
	}
	var deviceID string
	var link *devicev1.LinkState
	if phone != "" {
		resp, perr := conn.Device.LinkWithPhone(cmd.Context(), connect.NewRequest(&devicev1.LinkWithPhoneRequest{DeviceId: id, Phone: phone}))
		if perr != nil {
			return perr
		}
		deviceID, link = resp.Msg.GetDeviceId(), resp.Msg.GetLink()
	} else {
		resp, serr := conn.Device.StartLink(cmd.Context(), connect.NewRequest(&devicev1.StartLinkRequest{DeviceId: id}))
		if serr != nil {
			return serr
		}
		deviceID, link = resp.Msg.GetDeviceId(), resp.Msg.GetLink()
	}
	if err := printLink(cmd, link); err != nil {
		return err
	}
	final, err := waitForLink(cmd.Context(), cmd, conn, deviceID, link.GetQr(), devicePollEvery)
	if err != nil {
		return err
	}
	if err := renderPairOutcome(cmd, deviceID, final.GetOutcome()); err != nil {
		return err
	}
	if final.GetOutcome() != "connected" {
		return errors.New("device pair: the attempt ended " + final.GetOutcome() + "; run `openwa device pair` again")
	}
	return nil
}

func renderPairOutcome(cmd *cobra.Command, deviceID, outcome string) error {
	if render.Detect(cmd) != render.FormatText {
		return render.JSON(cmd.OutOrStdout(), map[string]any{"device_id": deviceID, "outcome": outcome})
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Pairing %s: %s\n", deviceID, outcome)
	return err
}

func waitForLink(ctx context.Context, cmd *cobra.Command, conn *controlplane.Client, deviceID, lastQR string, every time.Duration) (*devicev1.LinkState, error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		resp, err := conn.Device.GetLinkState(ctx, connect.NewRequest(&devicev1.GetLinkStateRequest{DeviceId: deviceID}))
		if err != nil {
			if ctx.Err() != nil {
				return nil, &PairInterruptedError{}
			}
			return nil, err
		}
		link := resp.Msg.GetLink()
		if link.GetOutcome() != "pending" {
			return link, nil
		}
		if q := link.GetQr(); q != "" && q != lastQR {
			lastQR = q
			if err := printLink(cmd, link); err != nil {
				return nil, err
			}
		}
		select {
		case <-ctx.Done():
			return nil, &PairInterruptedError{}
		case <-t.C:
		}
	}
}

func printLink(cmd *cobra.Command, link *devicev1.LinkState) error {
	out := cmd.ErrOrStderr()
	if code := link.GetPairingCode(); code != "" {
		_, err := fmt.Fprintf(out, "On the phone choose to link with a phone number, then enter: %s\n", code)
		return err
	}
	if link.GetQr() == "" {
		_, err := fmt.Fprintln(out, "Waiting for a QR code…")
		return err
	}
	q, err := qrcode.New(link.GetQr(), qrcode.Medium)
	if err != nil {
		return fmt.Errorf("device pair: render qr: %w", err)
	}
	_, err = fmt.Fprintf(out, "%s\nScan with WhatsApp: Settings, Linked devices, Link a device.\n", q.ToSmallString(false))
	return err
}

func newDeviceLogoutCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log the device's WhatsApp account out; the device stays",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			id, err := deviceIDFrom(cmd, conn, false)
			if err != nil {
				return err
			}
			resp, err := conn.Device.Unlink(cmd.Context(), connect.NewRequest(&devicev1.UnlinkRequest{DeviceId: id}))
			if err != nil {
				return err
			}
			if render.Detect(cmd) != render.FormatText {
				return render.JSON(cmd.OutOrStdout(), map[string]any{"device_id": resp.Msg.GetDeviceId(), "logged_out": true})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Logged out device %s\n", resp.Msg.GetDeviceId())
			return err
		},
	}
}

func newDeviceDeleteCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "delete",
		Short: "Log out and delete a device (--device is required)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			id, err := deviceIDFrom(cmd, conn, true)
			if err != nil {
				return err
			}
			if _, err := conn.Device.DeleteDevice(cmd.Context(), connect.NewRequest(&devicev1.DeleteDeviceRequest{DeviceId: id})); err != nil {
				return err
			}
			if render.Detect(cmd) != render.FormatText {
				return render.JSON(cmd.OutOrStdout(), map[string]any{"device_id": id, "deleted": true})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted device %s\n", id)
			return err
		},
	}
}

func deviceIDFrom(cmd *cobra.Command, conn *controlplane.Client, required bool) (string, error) {
	raw, _ := cmd.Flags().GetString("device")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return "", errors.New("device: --device is required (a name or a dev_ id; `openwa device list` shows them)")
		}
		return "", nil
	}
	if publicid.Valid(devicePrefix, raw) {
		return raw, nil
	}
	resp, err := conn.Device.ListDevices(cmd.Context(), connect.NewRequest(&devicev1.ListDevicesRequest{}))
	if err != nil {
		return "", err
	}
	for _, d := range resp.Msg.GetDevices() {
		if strings.EqualFold(d.GetName(), raw) {
			return d.GetId(), nil
		}
	}
	return "", fmt.Errorf("device: no device named %q in the active project", raw)
}

func renderDevice(cmd *cobra.Command, d *devicev1.Device) error {
	if render.Detect(cmd) != render.FormatText {
		return render.JSON(cmd.OutOrStdout(), deviceMap(d))
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"id:        %s\nname:      %s\nstate:     %s\nphone:     %s\npush name: %s\nversion:   %d\n",
		d.GetId(), d.GetName(), d.GetState(), phoneText(d.GetPhone()), d.GetPushName(), d.GetVersion())
	return err
}

func deviceMap(d *devicev1.Device) map[string]any {
	out := map[string]any{
		"id":        d.GetId(),
		"name":      d.GetName(),
		"state":     d.GetState(),
		"phone":     d.GetPhone(),
		"push_name": d.GetPushName(),
		"version":   d.GetVersion(),
	}
	if ts := d.GetLastSeenAt(); ts != nil {
		out["last_seen_at"] = rfc3339UTC(ts.AsTime())
	}
	return out
}

func phoneText(p string) string {
	if p == "" {
		return "—"
	}
	return "+" + p
}

package cli

import (
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	messagev1 "altalune.id/openwa/gen/go/message/v1"
)

type sendFlags struct {
	to, text, replyTo string
	mentions          []string
	markRead          bool
}

func newSendCmd(bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "send",
		Short:   "Queue a WhatsApp message (text, image, document or location)",
		Long:    "Queue a message over the control plane (S2). The command returns once the message is queued; its status moves to sent, delivered and read afterwards (see `openwa message list`).",
		GroupID: "domain",
	}
	addDeviceFlag(cmd)
	cmd.AddCommand(newSendTextCmd(bootClient), newSendImageCmd(bootClient), newSendDocumentCmd(bootClient), newSendLocationCmd(bootClient))
	return cmd
}

func addSendFlags(cmd *cobra.Command, f *sendFlags) {
	cmd.Flags().StringVar(&f.to, "to", "", "recipient: phone number with country code (e.g. 628123456789) or a JID (required)")
	cmd.Flags().StringVar(&f.replyTo, "reply-to", "", "message id (ours or WhatsApp's) to quote")
	cmd.Flags().StringSliceVar(&f.mentions, "mention", nil, "phone number mentioned as @<digits> in the text; repeatable")
	cmd.Flags().BoolVar(&f.markRead, "mark-read", false, "mark the chat read before sending")
	_ = cmd.MarkFlagRequired("to")
}

func send(cmd *cobra.Command, bootClient ClientBootFn, f sendFlags, fill func(*messagev1.SendRequest)) error {
	conn, err := connFromCmd(cmd, bootClient)
	if err != nil {
		return err
	}
	deviceID, err := deviceIDFrom(cmd, conn, false)
	if err != nil {
		return err
	}
	req := &messagev1.SendRequest{DeviceId: deviceID, To: f.to, Text: f.text, ReplyTo: f.replyTo, Mentions: f.mentions, MarkReadFirst: f.markRead}
	fill(req)
	resp, err := conn.Message.Send(cmd.Context(), connect.NewRequest(req))
	if err != nil {
		if len(req.GetMedia().GetData()) > 0 && connect.CodeOf(err) == connect.CodeResourceExhausted {
			return fmt.Errorf("send: the server refused the file as too large (whatsapp.mediaMaxBytes); host it and send it with --url instead: %w", err)
		}
		return deviceRPCError(err)
	}
	m := resp.Msg.GetMessage()
	return one(cmd, messageMap(m), fmt.Sprintf("Queued message %s to %s (status %s)", m.GetId(), f.to, m.GetStatus()))
}

func newSendTextCmd(bootClient ClientBootFn) *cobra.Command {
	var f sendFlags
	cmd := &cobra.Command{
		Use:   "text",
		Short: "Send a text message",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(f.text) == "" {
				return errors.New("send text: --text is required")
			}
			return send(cmd, bootClient, f, func(*messagev1.SendRequest) {})
		},
	}
	addSendFlags(cmd, &f)
	cmd.Flags().StringVar(&f.text, "text", "", "message text (required)")
	return cmd
}

type mediaFlags struct {
	file, url, caption, filename, mime string
}

func mediaRequest(kind string, m mediaFlags) (*messagev1.MediaInput, error) {
	if (m.file == "") == (m.url == "") {
		return nil, fmt.Errorf("send %s: pass exactly one of --file or --url", kind)
	}
	in := &messagev1.MediaInput{Url: m.url, Caption: m.caption, Filename: m.filename, Mime: m.mime}
	if m.file == "" {
		return in, nil
	}
	data, sniffed, err := readMediaFile(m.file)
	if err != nil {
		return nil, err
	}
	in.Data = data
	if in.Mime == "" {
		in.Mime = sniffed
	}
	if in.Filename == "" {
		in.Filename = filepath.Base(m.file)
	}
	return in, nil
}

func newSendImageCmd(bootClient ClientBootFn) *cobra.Command {
	var f sendFlags
	var m mediaFlags
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Send an image from --file or --url",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			media, err := mediaRequest("image", m)
			if err != nil {
				return err
			}
			if media.GetMime() != "" && !strings.HasPrefix(media.GetMime(), "image/") {
				return fmt.Errorf("send image: %s is %s, not an image; use `send document`", media.GetFilename(), media.GetMime())
			}
			return send(cmd, bootClient, f, func(r *messagev1.SendRequest) { r.Media = media })
		},
	}
	addSendFlags(cmd, &f)
	cmd.Flags().StringVar(&m.file, "file", "", "local image file")
	cmd.Flags().StringVar(&m.url, "url", "", "public http(s) URL the server downloads")
	cmd.Flags().StringVar(&m.caption, "caption", "", "caption shown under the image")
	return cmd
}

func newSendDocumentCmd(bootClient ClientBootFn) *cobra.Command {
	var f sendFlags
	var m mediaFlags
	cmd := &cobra.Command{
		Use:   "document",
		Short: "Send a file as a document from --file or --url",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			media, err := mediaRequest("document", m)
			if err != nil {
				return err
			}
			if media.GetMime() == "" || media.GetMime() == "application/octet-stream" || strings.HasPrefix(media.GetMime(), "image/") {
				media.Mime = documentMime(media.GetFilename(), media.GetMime())
			}
			return send(cmd, bootClient, f, func(r *messagev1.SendRequest) { r.Media = media })
		},
	}
	addSendFlags(cmd, &f)
	cmd.Flags().StringVar(&m.file, "file", "", "local file")
	cmd.Flags().StringVar(&m.url, "url", "", "public http(s) URL the server downloads")
	cmd.Flags().StringVar(&m.filename, "filename", "", "file name the recipient sees (default: the file's base name)")
	cmd.Flags().StringVar(&m.caption, "caption", "", "caption shown with the document")
	cmd.Flags().StringVar(&m.mime, "mime", "", "mime type (default: sniffed, then by extension)")
	return cmd
}

func newSendLocationCmd(bootClient ClientBootFn) *cobra.Command {
	var f sendFlags
	var lat, lng float64
	var name, address string
	cmd := &cobra.Command{
		Use:   "location",
		Short: "Send a location pin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return send(cmd, bootClient, f, func(r *messagev1.SendRequest) {
				r.Location = &messagev1.Location{Lat: lat, Lng: lng, Name: name, Address: address}
			})
		},
	}
	addSendFlags(cmd, &f)
	cmd.Flags().Float64Var(&lat, "lat", 0, "latitude in degrees (required)")
	cmd.Flags().Float64Var(&lng, "lng", 0, "longitude in degrees (required)")
	cmd.Flags().StringVar(&name, "name", "", "place name")
	cmd.Flags().StringVar(&address, "address", "", "address line")
	_ = cmd.MarkFlagRequired("lat")
	_ = cmd.MarkFlagRequired("lng")
	return cmd
}

func documentMime(filename, sniffed string) string {
	if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); byExt != "" && !strings.HasPrefix(byExt, "image/") {
		return byExt
	}
	if sniffed == "" {
		return "application/octet-stream"
	}
	return sniffed
}

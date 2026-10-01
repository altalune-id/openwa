// Command webhook-pong is a localhost webhook receiver for manual testing. It verifies the
// X-Openwa-Signature, logs each delivery, always answers 200 with "PONG!!!" and a timestamp,
// and optionally replies "PONG!!!" in WhatsApp over the data plane.
//
// Run (print only, verify signatures):
//
//	OPENWA_WEBHOOK_SECRET=whsec_... go run ./examples/webhook-pong
//
// Run (also reply in WhatsApp, only to one sender). OPENWA_BASE_URL is the origin only and
// defaults to https://openwa.altalune.id; set it for a self-hosted or custom-domain instance:
//
//	OPENWA_WEBHOOK_SECRET=whsec_... \
//	OPENWA_BASE_URL=http://127.0.0.1:8200 \
//	OPENWA_ORG=<org-slug> OPENWA_PROJECT=<project-slug> \
//	OPENWA_KEY=key_... \
//	go run ./examples/webhook-pong -reply -only 628123456789
//
// Subscribe the webhook to message.matched (fires only when the device's rules pass), not
// message.received (fires for every inbound message); subscribing to both replies twice.
// See README.md for the flags and the localhost delivery note.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://openwa.altalune.id"

type options struct {
	addr    string
	secret  string
	reply   bool
	baseURL string
	org     string
	project string
	key     string
	only    string
}

type envelope struct {
	Type string `json:"type"`
	Data struct {
		Device  struct{ ID string }                      `json:"device"`
		Chat    struct{ ID, JID, Kind string }           `json:"chat"`
		Sender  struct{ JID, Phone, PushName string }    `json:"sender"`
		Message struct{ ID, Type, Body, Caption string } `json:"message"`
	} `json:"data"`
}

func main() {
	o := options{}
	flag.StringVar(&o.addr, "addr", envOr("ADDR", "127.0.0.1:9099"), "listen address")
	flag.StringVar(&o.secret, "secret", os.Getenv("OPENWA_WEBHOOK_SECRET"), "signing secret; when set, invalid signatures are rejected 401")
	flag.BoolVar(&o.reply, "reply", false, "send a WhatsApp reply via the data plane (default: print only)")
	flag.StringVar(&o.baseURL, "base-url", envOr("OPENWA_BASE_URL", defaultBaseURL), "openwa origin, e.g. http://127.0.0.1:8200")
	flag.StringVar(&o.org, "org", os.Getenv("OPENWA_ORG"), "org slug (for -reply)")
	flag.StringVar(&o.project, "project", os.Getenv("OPENWA_PROJECT"), "project slug (for -reply)")
	flag.StringVar(&o.key, "key", os.Getenv("OPENWA_KEY"), "device API key with messages:write (for -reply)")
	flag.StringVar(&o.only, "only", "", "reply only to this sender phone or JID (default: everyone)")
	flag.Parse()

	if o.reply && (o.org == "" || o.project == "" || o.key == "") {
		log.Fatal("-reply needs -org, -project and -key (OPENWA_ORG / OPENWA_PROJECT / OPENWA_KEY)")
	}
	if o.secret == "" {
		log.Print("WARNING: no -secret set; signatures are not verified")
	}

	srv := &http.Server{Addr: o.addr, Handler: handler(o), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("webhook-pong listening on http://%s (reply=%v)", o.addr, o.reply)
	log.Fatal(srv.ListenAndServe())
}

func handler(o options) http.HandlerFunc {
	var replied sync.Map
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
		logDelivery(r, body)

		if o.secret != "" && !verify(o.secret, r.Header, body, time.Now()) {
			log.Print("  signature: INVALID -> 401")
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		if o.secret != "" {
			log.Print("  signature: ok")
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "PONG!!! %s\n", time.Now().Format(time.RFC3339Nano))

		if o.reply {
			go maybeReply(o, &replied, body)
		}
	}
}

func maybeReply(o options, replied *sync.Map, body []byte) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return
	}
	if env.Type != "message.received" && env.Type != "message.matched" {
		return
	}
	sender := env.Data.Sender.Phone
	if sender == "" {
		sender = env.Data.Sender.JID
	}
	if o.only != "" && o.only != env.Data.Sender.Phone && o.only != env.Data.Sender.JID {
		log.Printf("  reply skipped: sender %s not %s", sender, o.only)
		return
	}
	if _, dup := replied.LoadOrStore(env.Data.Message.ID, true); dup {
		return
	}
	if err := sendReply(o, env); err != nil {
		log.Printf("  reply FAILED: %v", err)
		return
	}
	log.Printf("  replied PONG!!! to %s in %s", sender, env.Data.Chat.JID)
}

func sendReply(o options, env envelope) error {
	payload, err := json.Marshal(map[string]any{
		"to":              env.Data.Chat.JID,
		"text":            fmt.Sprintf("PONG!!! %s", time.Now().Format(time.RFC3339Nano)),
		"reply_to":        env.Data.Message.ID,
		"mark_read_first": true,
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(o.baseURL, "/") + "/api/v1/orgs/" + o.org + "/projects/" + o.project + "/devices/" + env.Data.Device.ID + "/messages"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "pong-"+env.Data.Message.ID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("openwa answered %s: %s", resp.Status, msg)
	}
	return nil
}

func logDelivery(r *http.Request, body []byte) {
	log.Printf("--- %s %s ---", r.Method, r.URL.Path) //nolint:gosec // G706: this example deliberately echoes the raw delivery for inspection
	for _, h := range []string{"Content-Type", "User-Agent", "X-Openwa-Event-Type", "X-Openwa-Event-Id", "X-Openwa-Delivery-Id", "X-Openwa-Timestamp", "X-Openwa-Signature"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		if h == "X-Openwa-Signature" {
			v = "(present)" // do not log the signature value
		}
		log.Printf("  %s: %q", h, v) //nolint:gosec // G706: this example deliberately echoes the raw delivery for inspection
	}
	if len(body) > 0 {
		log.Printf("  body: %s", body) //nolint:gosec // G706: this example deliberately echoes the raw delivery body for inspection
	}
}

func verify(secret string, h http.Header, body []byte, now time.Time) bool {
	ts := h.Get("X-Openwa-Timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := []byte("v1=" + hex.EncodeToString(mac.Sum(nil)))
	for got := range strings.FieldsSeq(h.Get("X-Openwa-Signature")) {
		if hmac.Equal([]byte(got), want) {
			return true
		}
	}
	return false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

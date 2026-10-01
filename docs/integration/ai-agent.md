# Pattern: an AI agent on WhatsApp

This page wires an AI agent to a WhatsApp number through OpenWA: a person writes, the agent
answers in the same chat. It uses only the REST data plane and webhooks from the
[integration guide](README.md). The example is in Go and talks to an AG-UI style chat API; the
shape carries over to any agent that takes a thread id and a message and returns text.

## The flow

1. A person sends a message. OpenWA stores it and emits `message.received`, then, when the
   device's rules pass, `message.matched`.
2. Your receiver gets `message.matched`, verifies it, answers `200` at once, and queues the
   work.
3. The worker calls the agent with:
   - tenant: the OpenWA project (`tenant.project_id`)
   - actor: the person, `data.sender.phone` (or `data.sender.jid` when the phone is hidden)
   - thread: the chat, `data.chat.id`
   - the text: `data.message.body`, or `data.message.caption` for media
4. The worker sends the agent's answer with `POST /devices/{data.device.id}/messages`,
   `to = data.chat.jid`, `reply_to = data.message.id`, and an `Idempotency-Key` derived from
   the inbound message so a retried delivery does not answer twice.

Subscribe the endpoint to `message.matched` only. `message.received` also fires for every
matched message, so subscribing to both makes the agent answer twice.

## Set up

1. Pair a device (README, "Devices and pairing").
2. Set its rules so the agent speaks only when spoken to. In groups, answer mentions and
   replies only; in direct chats, every message:

   ```json
   {
     "group_mode": "mention",
     "allowed_senders": [],
     "allowed_groups": [],
     "trigger_prefix": "",
     "ignore_from_me": true
   }
   ```

3. Create an API key with `messages:write` (add `messages:read` if the agent reads media),
   bound to that device.
4. Create a webhook endpoint pointing at your receiver, subscribed to `message.matched`. Keep
   the secret.

## The receiver

```go
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type envelope struct {
	Type   string `json:"type"`
	Tenant struct {
		ProjectID string `json:"project_id"`
	} `json:"tenant"`
	Data matched `json:"data"`
}

type matched struct {
	Device struct {
		ID string `json:"id"`
	} `json:"device"`
	Chat struct {
		ID   string `json:"id"`
		JID  string `json:"jid"`
		Kind string `json:"kind"`
	} `json:"chat"`
	Sender struct {
		JID      string `json:"jid"`
		Phone    string `json:"phone"`
		PushName string `json:"push_name"`
	} `json:"sender"`
	Message struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Body    string `json:"body"`
		Caption string `json:"caption"`
	} `json:"message"`
}

type config struct {
	secret, openwaBase, openwaKey string
	agentURL, agentID             string
}

func main() {
	cfg := config{
		secret:     os.Getenv("OPENWA_WEBHOOK_SECRET"),
		openwaBase: os.Getenv("OPENWA_BASE"), // https://wa.example.com/api/v1/orgs/acme/projects/main
		openwaKey:  os.Getenv("OPENWA_KEY"),
		agentURL:   os.Getenv("AGENT_URL"), // https://agent.example.com/api/chat
		agentID:    os.Getenv("AGENT_ID"),
	}
	jobs := make(chan matchedJob, 256)
	go worker(cfg, jobs)

	var seen sync.Map
	http.HandleFunc("POST /openwa", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !verify(cfg.secret, r.Header, body, time.Now()) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		delivery := r.Header.Get("X-Openwa-Delivery-Id")
		if _, dup := seen.Load(delivery); dup {
			w.WriteHeader(http.StatusOK)
			return
		}
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil || env.Type != "message.matched" {
			w.WriteHeader(http.StatusOK)
			return
		}
		select {
		case jobs <- matchedJob{tenant: env.Tenant.ProjectID, data: env.Data}:
			seen.Store(delivery, true) // only once queued: a 503 below must let OpenWA's retry through
		default:
			http.Error(w, "busy", http.StatusServiceUnavailable) // OpenWA retries later
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	slog.Info("listening", "addr", ":8080")
	_ = http.ListenAndServe(":8080", nil) //nolint:gosec // example; put a real server with timeouts in production
}

type matchedJob struct {
	tenant string
	data   matched
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
	for _, got := range strings.Fields(h.Get("X-Openwa-Signature")) {
		if hmac.Equal([]byte(got), want) {
			return true
		}
	}
	return false
}

func worker(cfg config, jobs <-chan matchedJob) {
	for job := range jobs {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		if err := handle(ctx, cfg, job); err != nil {
			slog.Error("agent reply failed", "message", job.data.Message.ID, "err", err)
		}
		cancel()
	}
}

func handle(ctx context.Context, cfg config, job matchedJob) error {
	m := job.data
	text := m.Message.Body
	if text == "" {
		text = m.Message.Caption
	}
	if text == "" {
		text = "[" + m.Message.Type + "]"
	}
	actor := m.Sender.Phone
	if actor == "" {
		actor = m.Sender.JID
	}
	answer, err := askAgent(ctx, cfg, job.tenant, actor, m, text)
	if err != nil || strings.TrimSpace(answer) == "" {
		return err
	}
	return reply(ctx, cfg, m, answer)
}
```

## Calling the agent

An AG-UI style endpoint takes the run input as JSON and streams events as server-sent
events. The thread id is the OpenWA chat id, so the agent keeps one conversation per chat;
`forwardedProps.channel` tells the agent it is on WhatsApp (short, plain text, no Markdown
tables).

```go
func askAgent(ctx context.Context, cfg config, tenant, actor string, m matched, text string) (string, error) {
	input := map[string]any{
		"threadId": m.Chat.ID,
		"runId":    m.Message.ID,
		"messages": []map[string]any{{"id": m.Message.ID, "role": "user", "content": text}},
		"tools":    []any{},
		"context":  []any{},
		"state":    map[string]any{},
		"forwardedProps": map[string]any{
			"channel":    "whatsapp",
			"chatKind":   m.Chat.Kind,
			"senderName": m.Sender.PushName,
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.agentURL, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("x-tenant-id", tenant)
	req.Header.Set("x-agent-id", cfg.agentID)
	req.Header.Set("x-actor-id", actor)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("agent answered %s", resp.Status)
	}
	var out strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		payload, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if json.Unmarshal([]byte(payload), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "TEXT_MESSAGE_CONTENT":
			out.WriteString(ev.Delta)
		case "RUN_ERROR":
			return "", fmt.Errorf("agent run failed: %s", payload)
		}
	}
	return out.String(), sc.Err()
}
```

If your agent answers with plain JSON instead of a stream, read the text from its response
body; nothing else changes.

## Replying

```go
func reply(ctx context.Context, cfg config, m matched, answer string) error {
	body, err := json.Marshal(map[string]any{
		"to":              m.Chat.JID,
		"text":            answer,
		"reply_to":        m.Message.ID,
		"mark_read_first": true,
	})
	if err != nil {
		return err
	}
	url := cfg.openwaBase + "/devices/" + m.Device.ID + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.openwaKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "agent-reply-"+m.Message.ID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("openwa answered %s: %s", resp.Status, msg)
	}
	return nil
}
```

`to` is the chat's JID, so a group message is answered in the group and a direct message in
the direct chat. `reply_to` quotes the person's message, which matters in a busy group.
`mark_read_first` shows the person blue ticks before the answer arrives.

## Running it

```bash
export OPENWA_WEBHOOK_SECRET=whsec_...
export OPENWA_BASE=https://wa.example.com/api/v1/orgs/acme/projects/main
export OPENWA_KEY=key_...
export AGENT_URL=https://agent.example.com/api/chat
export AGENT_ID=support-bot
go run .
```

Expose port 8080 over `https` (a reverse proxy or a tunnel), point the webhook endpoint at
`https://<host>/openwa`, and send the number a message from another phone. The console's
webhook page shows each delivery and your receiver's answer; the inbox shows the agent's
reply in the thread.

## Production notes

- The in-memory `seen` map and the job channel are per process. With several replicas, dedupe
  in a shared store (a unique key on `X-Openwa-Delivery-Id`) and use a real queue.
- Keep one run per chat at a time (a per-`chat.id` lock), or two quick messages produce two
  overlapping answers.
- Long answers: WhatsApp shows up to 65,536 characters, but people read short ones. Ask the
  agent for short replies through `forwardedProps.channel`.
- Media: `data.message.media.url` needs the key's `messages:read`; download it and pass it to
  the agent if it can see images.
- Handover: when a human takes over, change the device's rules (for example set
  `allowed_senders` to exclude that person) or stop the worker for that chat; the inbox keeps
  working for the human.

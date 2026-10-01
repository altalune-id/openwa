# Integrating with OpenWA

This guide is for a developer who connects a system (a CRM, a help desk, an AI agent) to
WhatsApp through OpenWA. Everything here goes through the REST data plane under
`/api/v1/orgs/{org}/projects/{project}/` and through outbound webhooks. The worked AI-agent
example is in [`ai-agent.md`](ai-agent.md).

In the examples:

```bash
export OPENWA=https://wa.example.com
export BASE="$OPENWA/api/v1/orgs/acme/projects/main"
export KEY=key_...          # an API key, see Authentication
export DEVICE=dev_V1StGXR8Z5jdHi6B
export CHAT=cht_Bd7Kq2Wm9Xp4Lz3a
```

Every id you see or send is a public id with a type prefix: `dev_` for a device, `cht_` for a
chat, `msg_` for a message. They are stable and safe to store. A contact has no id of its own:
it is its `jid` on a device. An id with the wrong prefix or shape answers `404`. A `next_cursor` is different: it is an opaque token that may embed internal ids, so pass it back as it is and never parse it.

## Authentication

Every call carries a credential as a bearer token: `Authorization: Bearer $KEY`. Only an org
owner or admin can create keys, in the console. Every key must be given an expiry when it
is created, at most one year out; there is no non-expiring key.

There are three kinds of credential:

- **Project key** — scoped to one project. Its calls resolve against that project, so the REST
  path's project must match. This is the usual integration credential.
- **Org key** — scoped to the whole org, with no active project. It must name the project it
  acts on (the REST path already does; on MCP pass `projectId`, on the CLI pass `--project`).
  A request that omits the project answers `PRJ005`.
- **Personal access token (PAT)** — acts as the person who created it, across the projects
  they can reach, with no active project. It names the project the same way an org key does
  (omitting it answers `PRJ005`), and it stops working the moment that person loses access to
  the org. For high send volume prefer a project or org key: a PAT re-checks membership on
  every request.

Create a project key in the console: open the project, **API keys**, **New key**. Pick only
the scopes the integration needs, and an expiry. The key is shown once.

| Scope            | Allows                                                      |
| ---------------- | ----------------------------------------------------------- |
| `devices:read`   | list devices, read their state and the current pairing      |
| `devices:write`  | create, rename, delete, pair and log out devices; set rules |
| `messages:read`  | list and read messages, download media                      |
| `messages:write` | send, react, revoke, edit, mark read                        |
| `chats:read`     | list and read chats, read group details                     |
| `chats:write`    | mark a chat read, join and leave groups                     |
| `contacts:read`  | list contacts                                               |

A **project key** can also be **bound to devices** (org keys and PATs cannot). A bound key
reaches only those devices, their messages and their chats; any other id answers
`404 not_found` as if it did not exist. Use one bound key per customer or per bot when several
share a project. A bound key works on this REST API only: the control plane and MCP enter
through the project as a whole, which a bound key cannot reach, so they refuse it with
`permission denied`.

Errors are JSON `{"code": "...", "message": "..."}` with these codes:

| Status | `code`                  | Meaning                                                                      |
| ------ | ----------------------- | ---------------------------------------------------------------------------- |
| 400    | `bad_request`           | the body or a parameter is malformed or invalid                              |
| 401    | `unauthorized`          | no key, or the key is unknown or revoked                                     |
| 404    | `not_found`             | no such resource, or the key may not see it (also a missing scope)           |
| 409    | `conflict`              | the state forbids it: device not linked, edit window closed, not a group     |
| 409    | `in_progress`           | the same `Idempotency-Key` is still being processed                          |
| 410    | `gone`                  | the media is no longer available on WhatsApp                                 |
| 412    | `precondition_failed`   | `If-Match` names an older version than the one stored                        |
| 413    | `payload_too_large`     | the body or the file is over the limit (`whatsapp.mediaMaxBytes`, 32 MiB)    |
| 428    | `precondition_required` | a `PATCH` came without `If-Match`                                            |
| 503    | `unavailable`           | the device's WhatsApp session is not running on this server right now; retry |

## Devices and pairing

A device is one WhatsApp account linked to OpenWA as a companion device, like WhatsApp Web.

```bash
# create
curl -sS -X POST "$BASE/devices" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -d '{"name":"sales-01"}'

# start pairing by QR code
curl -sS -X POST "$BASE/devices/$DEVICE/links" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -d '{"method":"qr"}'
```

The link resource has `status` (`pending`, `connected`, `timeout`, `failed`), `qr_png_base64`
(a PNG to show the user), `expires_at` and `url`. WhatsApp rotates the QR code about every
20 seconds, so poll `GET $BASE/devices/$DEVICE/links/current` every few seconds and show the
new PNG until `status` is `connected`. On the phone: **Settings**, **Linked devices**,
**Link a device**.

To pair without a camera, send `{"method":"phone","phone":"+628123456789"}`. The link then
carries an 8-character `code`; on the phone choose **Link with phone number instead** and type
it.

`GET $BASE/devices/$DEVICE` shows `state` (`linking`, `connected`, `disconnected`, `logged_out`,
`unlinked`), `phone` and `last_seen_at`. `POST $BASE/devices/$DEVICE/unlink` logs the account
out and keeps the device and its history. Subscribe to `device.connected`,
`device.disconnected` and `device.logged_out` to watch it; see
[`webhooks`](../webhooks/README.md#events).

## Sending

`POST $BASE/devices/$DEVICE/messages` queues a message and answers `202 Accepted` with the
message. The message's `status` then moves `queued` → `sending` → `sent` → `delivered` →
`read` (or `played` for a voice note), or ends `failed` with an `error`. A device sends one
message at a time, with a random 1 to 3 second gap and a typing indicator before text, so a
burst takes a while to drain; that pacing is on purpose (see [Rate and anti-ban](#rate-and-anti-ban)).

`to` is a phone number with its country code and no `+` (`628111222333`; spaces, dashes and a
leading `+` are removed) or a JID (`628111222333@s.whatsapp.net`, a group
`120363012345678901@g.us`). OpenWA does not check that the number uses WhatsApp before it
queues the message; such a message ends `failed` or never reaches `delivered`. The console's
**New chat** checks the number first when you are unsure.

Send `Idempotency-Key: <your id>` to make a retry safe: the same key with the same body
within 24 hours returns the first response and sends nothing new. The key is remembered by
the server process that took it, so keep your own dedupe too when you run several replicas.

Text:

```bash
curl -sS -X POST "$BASE/devices/$DEVICE/messages" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: order-1042-confirm' \
  -d '{"to":"628111222333","text":"Your order #1042 is confirmed."}'
```

Image by URL (OpenWA downloads it; only public `http(s)` addresses are fetched):

```bash
curl -sS -X POST "$BASE/devices/$DEVICE/messages" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"to":"628111222333","media":{"url":"https://files.example.com/invoice.png","caption":"Invoice for September"}}'
```

Image inline, base64 in JSON:

```bash
curl -sS -X POST "$BASE/devices/$DEVICE/messages" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"to":"628111222333","media":{"base64":"'"$(base64 < invoice.png | tr -d '\n')"'","mime":"image/png","filename":"invoice.png"}}'
```

Any file as a multipart upload (the type follows the file's `Content-Type`: `image/*` sends an
image, `video/*` a video, `audio/*` audio, anything else a document):

```bash
curl -sS -X POST "$BASE/devices/$DEVICE/messages" -H "Authorization: Bearer $KEY" \
  -F to=628111222333 -F caption='Signed contract' -F 'file=@contract.pdf;type=application/pdf'
```

Voice note (Opus in Ogg; plays as a voice note, not a file):

```bash
curl -sS -X POST "$BASE/devices/$DEVICE/messages" -H "Authorization: Bearer $KEY" \
  -F to=628111222333 -F voice=true -F 'file=@reply.ogg;type=audio/ogg'
```

Location:

```bash
curl -sS -X POST "$BASE/devices/$DEVICE/messages" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"to":"628111222333","location":{"lat":-6.2088,"lng":106.8456,"name":"Office","address":"Jl. Sudirman 1, Jakarta"}}'
```

Reply and mention. `reply_to` is a message id (`msg_…`) or a WhatsApp `wa_id`. A mention is the
phone number in `mentions` and `@<digits>` in the text:

```bash
curl -sS -X POST "$BASE/devices/$DEVICE/messages" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"to":"120363012345678901@g.us","text":"@628111222333 sudah dikirim","mentions":["628111222333"],"reply_to":"msg_Mj6Zc1Ld9Fs4Kb2c"}'
```

Set `"mark_read_first": true` to send read receipts for the chat before the reply goes out.

Acting on a message (each answers `202` with the new queued message, except `read`, `204`):

```bash
MSG=msg_Mj6Zc1Ld9Fs4Kb2c
curl -sS -X POST "$BASE/messages/$MSG/reactions" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -d '{"emoji":"👍"}'       # "" removes the reaction
curl -sS -X POST "$BASE/messages/$MSG/revoke" -H "Authorization: Bearer $KEY"   # delete for everyone; ours only
curl -sS -X PATCH "$BASE/messages/$MSG" -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -d '{"text":"fixed typo"}'  # ours only, within 20 minutes
curl -sS -X POST "$BASE/chats/$CHAT/read" -H "Authorization: Bearer $KEY"       # read receipts
```

Groups: `POST $BASE/devices/$DEVICE/groups` with `{"invite_link":"https://chat.whatsapp.com/…"}`
joins and answers `201` with the chat; `GET $BASE/chats/$CHAT/group` returns the name, topic,
participant count and invite link; `POST $BASE/chats/$CHAT/leave` leaves and archives the chat.
Send to a group with its JID as `to`.

## Reading

| Route                                                              | Returns                                |
| ------------------------------------------------------------------ | -------------------------------------- |
| `GET /chats?device_id=&kind=dm\|group&q=&cursor=&limit=`           | chats, most recent activity first      |
| `GET /chats/{chat}`                                                | one chat                               |
| `GET /chats/{chat}/messages?since_id=&cursor=&limit=`              | the chat's messages, newest first      |
| `GET /devices/{device}/messages?chat_id=&since_id=&cursor=&limit=` | the device's messages, newest first    |
| `GET /messages/{message}`                                          | one message, with its current `status` |
| `GET /contacts?device_id=&q=&cursor=&limit=`                       | contacts, most recently updated first  |

Lists answer `{"data": [...], "next_cursor": "..."}`. Pass `next_cursor` back as `cursor` for
the next page; an empty `next_cursor` is the last page. `limit` is 1 to 200 (default 50).
`since_id=<msg_…>` instead returns the messages newer than that one, oldest first, which
is the easy way to poll a thread.

## Webhooks

Create an endpoint in the console: open the project, **Webhooks**, **New endpoint**, give an
`https://` URL and pick the events. The signing secret (`whsec_…`) is shown once.

Every delivery is a JSON envelope `{id, type, api_version, created_at, tenant, data}` with the
headers `X-Openwa-Event-Id`, `X-Openwa-Event-Type`, `X-Openwa-Delivery-Id`,
`X-Openwa-Timestamp` and `X-Openwa-Signature`.

Verify every delivery before you trust it. The signature is
`v1=` + hex `HMAC-SHA256(secret, timestamp + "." + raw_body)`, with the secret string taken
as-is, `whsec_` included:

```go
func Verify(secret string, h http.Header, body []byte, now time.Time) bool {
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
```

```js
import crypto from "node:crypto";

export function verify(secret, headers, rawBody, nowMs = Date.now()) {
  const ts = headers["x-openwa-timestamp"] ?? "";
  if (!/^\d+$/.test(ts) || Math.abs(nowMs / 1000 - Number(ts)) > 300)
    return false;
  const mac = crypto
    .createHmac("sha256", secret)
    .update(`${ts}.`)
    .update(rawBody);
  const want = Buffer.from(`v1=${mac.digest("hex")}`);
  return (headers["x-openwa-signature"] ?? "").split(" ").some((sig) => {
    const got = Buffer.from(sig);
    return got.length === want.length && crypto.timingSafeEqual(got, want);
  });
}
```

During a secret rotation the header carries two signatures; accept either.

Delivery is **at least once**. The same delivery can arrive twice, so dedupe on
`X-Openwa-Delivery-Id`. Answer `2xx` within 10 seconds and do the work afterwards; anything
else (a timeout, a `3xx`, a `5xx`) counts as a failed attempt; a delivery gets 8 attempts in all, over about 28 hours, with growing waits.
Deliveries are written in the same database transaction as the change they describe (an
outbox), so an event is never lost and never sent for a change that rolled back. No order is
promised between deliveries: sort by `data.message.timestamp` or `data.at` if you need one.
The full contract is [`webhooks`](../webhooks/README.md).

## Events

| Type                                                           | Fires when                                                               |
| -------------------------------------------------------------- | ------------------------------------------------------------------------ |
| `message.received`                                             | a device receives a message from someone else                            |
| `message.matched`                                              | a received message also passes the device's inbound rules                |
| `message.status`                                               | one of our messages is `sent`, `delivered`, `read`, `played` or `failed` |
| `device.connected`, `device.disconnected`, `device.logged_out` | the device's session changes                                             |

`data` of `message.received`:

<!-- prettier-ignore -->
```json golden=message_received_v1
{
  "device": {
    "id": "dev_V1StGXR8Z5jdHi6B",
    "name": "sales-01",
    "phone": "628123456789"
  },
  "chat": {
    "id": "cht_Bd7Kq2Wm9Xp4Lz3a",
    "jid": "628111222333@s.whatsapp.net",
    "kind": "dm",
    "name": "Budi"
  },
  "sender": {
    "jid": "12345678901234@lid",
    "lid": "12345678901234@lid",
    "phone": "628111222333",
    "push_name": "Budi",
    "from_me": false
  },
  "message": {
    "id": "msg_Hy3Rk8Pw2Nq5Tv7a",
    "wa_id": "3A5F0C1D2E3F4A5B6C7D",
    "type": "text",
    "body": "halo, stok masih ada?",
    "caption": "",
    "timestamp": "2026-09-28T10:15:00Z",
    "quoted": null,
    "mentions": [],
    "media": null,
    "location": null
  }
}
```

`data` of `message.matched` (the same shape plus `matched.reasons`):

<!-- prettier-ignore -->
```json golden=message_matched_v1
{
  "device": {
    "id": "dev_V1StGXR8Z5jdHi6B",
    "name": "sales-01",
    "phone": "628123456789"
  },
  "chat": {
    "id": "cht_Gr5Tn8Vc1Hs6Jq0b",
    "jid": "120363012345678901@g.us",
    "kind": "group",
    "name": "Tim Sales"
  },
  "sender": {
    "jid": "628111222333@s.whatsapp.net",
    "lid": "12345678901234@lid",
    "phone": "628111222333",
    "push_name": "Budi",
    "from_me": false
  },
  "message": {
    "id": "msg_Mj6Zc1Ld9Fs4Kb2c",
    "wa_id": "3A5F0C1D2E3F4A5B6C7E",
    "type": "image",
    "body": "",
    "caption": "@628123456789 cek ini",
    "timestamp": "2026-09-28T10:16:00Z",
    "quoted": {
      "wa_id": "3EB00199C1F000007000800000000000E000",
      "body": "kirim foto produk"
    },
    "mentions": [
      "628123456789"
    ],
    "media": {
      "mime": "image/jpeg",
      "size": 48213,
      "filename": "",
      "url": "https://wa.example.com/api/v1/orgs/acme/projects/main/messages/msg_Mj6Zc1Ld9Fs4Kb2c/media",
      "voice": false
    },
    "location": null
  },
  "matched": {
    "reasons": [
      "group_mention"
    ]
  }
}
```

`data` of `message.status`:

<!-- prettier-ignore -->
```json golden=message_status_v1
{
  "device": {
    "id": "dev_V1StGXR8Z5jdHi6B",
    "name": "sales-01",
    "phone": "628123456789"
  },
  "message": {
    "id": "msg_St0Xw7Ge3Ua8Yi1d",
    "wa_id": "3EB00199C1F000007000800000000000E003"
  },
  "status": "read",
  "at": "2026-09-28T10:17:00Z"
}
```

Field notes:

- `sender.phone` is digits without `+`. It is empty when WhatsApp hides the number and the
  sender is known only by a LID (`…@lid`); `sender.jid` is then the LID. Key your records on
  `sender.phone` when it is set, else on `sender.jid`.
- `chat.id` is stable for the life of the chat; use it as your conversation id.
- `message.body` is the text; for media, the caption is in `message.caption`.
- `message.type` is `text`, `image`, `video`, `audio`, `document`, `location`, `contact`,
  `reaction`, `revoke`, `edit` or `unknown`.
- `message.status` fires once per step, and `delivered` or `read` never goes back. `failed`
  fires only when the send cannot succeed (WhatsApp refused it, for example with its
  new-contact throttle) or its three attempts ran out. A device that is offline keeps its
  messages `queued` until it reconnects.
- A message the account sends from the phone is stored and shows in lists with
  `from_me: true`, but emits no event.

## Media

`message.media.url` points at `GET /messages/{message}/media` on the data plane. It needs the same
bearer key, with `messages:read` and access to the device:

```bash
curl -sS -o photo.jpg "$BASE/messages/$MSG/media" -H "Authorization: Bearer $KEY"
```

The response carries the file's `Content-Type`, `Cache-Control: no-store`,
`X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`. JPEG, PNG, WebP and GIF
are served `inline`; everything else, SVG included, as an `attachment`. The URL's host is the
server's `http.baseURL` (`OPENWA_HTTP_BASE_URL`). OpenWA keeps the download keys, not the file:
each request fetches the file from WhatsApp and streams it. Sent media gets a URL too (its upload
keys are stored when WhatsApp acknowledges the send). Sent and received media both answer
`410 gone` once WhatsApp's CDN drops the file, and every URL stops working when retention
deletes the message (30 days by default, set in **Settings**), so download what you keep when
the event arrives. A file larger than `whatsapp.mediaMaxBytes` is refused with
`413 payload_too_large` before any download.

## Inbound rules: `received` versus `matched`

Every inbound message emits `message.received`. The device's inbound rules decide whether it
also emits `message.matched`, which is what an automated responder should listen to. Set them
in the console (device, **Rules**) or with
`PATCH $BASE/devices/$DEVICE` (`If-Match` with the device's `ETag`):

```bash
curl -sS -X PATCH "$BASE/devices/$DEVICE" -H "Authorization: Bearer $KEY" -H "If-Match: $ETAG" \
  -H 'Content-Type: application/json' \
  -d '{"rules":{"group_mode":"mention","allowed_senders":[],"allowed_groups":[],"trigger_prefix":"","ignore_from_me":true}}'
```

| Rule              | Effect                                                                                                                        |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `group_mode`      | `ignore`: no group message matches; `mention`: only when the account is @mentioned or replied to; `open`: every group message |
| `allowed_senders` | when set, only these phone numbers match, in chats and groups                                                                 |
| `allowed_groups`  | when set, only these group JIDs match                                                                                         |
| `trigger_prefix`  | when set, the text must start with it (case-insensitive, after trimming spaces)                                               |
| `ignore_from_me`  | messages from the account itself never match                                                                                  |

`matched.reasons` names what passed: `dm`, `sender_allowed`, `prefix`, `group_mention`,
`group_reply`, `group_open`. Direct messages always pass `group_mode`.

## Rate and anti-ban

WhatsApp bans accounts that behave like bulk senders. OpenWA paces each device (one message at
a time, 1 to 3 seconds apart, a typing indicator before text), but the pattern of use is yours:

- Message people who wrote to you or opted in. Unsolicited messages to strangers get reported,
  and reports ban numbers.
- Warm a new number up: a few conversations a day for the first week, not hundreds.
- Do not send the same text to many people; personalise, and spread campaigns over hours.
- Reply in the conversation (`reply_to`) instead of opening new ones.
- Honour "stop" at once, and keep one device per phone number.
- Watch `message.status`: many sends stuck at `sent`, never `delivered`, means WhatsApp is
  throttling you. Slow down.
- For marketing at scale use the official WhatsApp Business Platform; OpenWA drives a companion
  device and is meant for conversations.

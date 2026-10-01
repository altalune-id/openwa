# webhook-pong

A localhost webhook receiver for manual testing. It:

- **verifies** the `X-Openwa-Signature` HMAC (when a secret is given),
- **logs** each delivery (method, openwa headers, body),
- always answers `200` with `PONG!!!` and a timestamp (what openwa records as the delivery result),
- optionally **replies in WhatsApp** by calling the data-plane send API.

The HTTP response body (`PONG!!!`) is only what openwa stores as the delivery result — it is
**not** a WhatsApp message. To actually reply in the chat, run with `-reply`, which makes a
separate `POST /devices/{id}/messages` call back to openwa.

## Flags

| Flag        | Env                     | Meaning                                                                                                               |
| ----------- | ----------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `-addr`     | `ADDR`                  | listen address (default `127.0.0.1:9099`)                                                                             |
| `-secret`   | `OPENWA_WEBHOOK_SECRET` | signing secret; when set, invalid signatures are rejected `401`                                                       |
| `-reply`    |                         | send a WhatsApp reply via the data plane (default: print only)                                                        |
| `-base-url` | `OPENWA_BASE_URL`       | openwa origin (default `https://openwa.altalune.id`); set for self-hosted/custom domain, e.g. `http://127.0.0.1:8200` |
| `-org`      | `OPENWA_ORG`            | org slug (for `-reply`)                                                                                               |
| `-project`  | `OPENWA_PROJECT`        | project slug (for `-reply`)                                                                                           |
| `-key`      | `OPENWA_KEY`            | device API key with `messages:write` (for `-reply`)                                                                   |
| `-only`     |                         | reply only to this sender phone or JID (default: everyone)                                                            |

The reply URL is built as `{base-url}/api/v1/orgs/{org}/projects/{project}/devices/{device}/messages`,
where `{device}` comes from the webhook payload. (The data plane resolves org/project by **slug**,
and the payload carries the project slug but only the org **id**, so the org slug is configured here.)

## Run

```bash
# print-only, verify signatures
OPENWA_WEBHOOK_SECRET=whsec_... go run ./examples/webhook-pong

# also reply "PONG!!!" in WhatsApp, but only to one sender
OPENWA_WEBHOOK_SECRET=whsec_... \
OPENWA_BASE_URL=http://127.0.0.1:8200 \
OPENWA_ORG=<org-slug> OPENWA_PROJECT=<project-slug> \
OPENWA_KEY=key_... \
go run ./examples/webhook-pong -reply -only 628123456789
```

## Point a webhook at it

In the console: **Project → Webhooks → New endpoint**, set the URL to your receiver, subscribe
to **`message.received`** (or `message.matched`), and save. Copy the signing secret (shown
once) into `OPENWA_WEBHOOK_SECRET`.

> **Localhost note.** openwa only sends webhooks to **https** URLs and its delivery client
> refuses loopback/private addresses (SSRF hardening), so it cannot reach
> `http://127.0.0.1:9099` out of the box. Reaching a local receiver needs either an https
> tunnel with a public host, or a self-hosted dev override. The **reply** direction
> (this example → openwa) is a normal API call and works against a local openwa.

## Signature

`X-Openwa-Signature` is `v1=` + `HMAC-SHA256(secret, timestamp + "." + rawBody)`, where the
timestamp is `X-Openwa-Timestamp`. The `verify` function in `main.go` also rejects timestamps
more than 5 minutes off.

# CLI commands

Every command, its args, flags, printed contract and payload fields. Surface and exit codes:
[`cli`](README.md). Global flags: [`resolution`](resolution.md).

## Commands

Group is the `--help` heading. `project` and `invite` act on the caller's active org, taken from
the session principal — **not** from `--org`.

| Group   | Command                                                 | Args           | Flags                                                                                                                                    | Contract / prints                                                                                                                    |
| ------- | ------------------------------------------------------- | -------------- | ---------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| Runtime | `init`                                                  | none           | `--email` (required), `--name` (defaults to `--email`), `--org-slug` (empty: generated), `--org-name`, `--project-slug`, `--interactive` | First-run bootstrap: creates the genesis admin and marks onboarding complete. Exits `6` when already onboarded (`ONB002`).           |
| Runtime | `serve`                                                 | none           | `--no-scheduler`, `--scheduler-only`, `--no-consumer`, `--consumer-only` — exclusions below                                              | Runs the HTTP listener on `http.addr` and blocks until signalled.                                                                    |
| Runtime | `migrate up`                                            | none           | —                                                                                                                                        | Applies every pending migration. Prints `migrations: up-to-date`.                                                                    |
| Runtime | `migrate status`                                        | none           | —                                                                                                                                        | One row per migration: `<version> <applied-at \| "pending"> <source>`.                                                               |
| Runtime | `migrate down-to <version>`                             | exactly 1, int | —                                                                                                                                        | Rolls the schema back to that goose version.                                                                                         |
| Runtime | `scheduler list`                                        | none           | —                                                                                                                                        | Lists registered jobs from the wired runner. Runs nothing.                                                                           |
| Runtime | `scheduler run <job>`                                   | exactly 1      | —                                                                                                                                        | Runs one job now, bypassing its schedule. Tenant-scoped jobs still fan out; singletons still take the cross-process lock.            |
| Auth    | `auth login`                                            | none           | `--admin`, `--print-token`, `--email`, `--password-stdin`                                                                                | OIDC loopback (RFC 8252) when `oidc.issuer` + `oidc.clientID` are set; local genesis prompt otherwise.                               |
| Auth    | `auth logout`                                           | none           | —                                                                                                                                        | Removes the file at `session.path`.                                                                                                  |
| Auth    | `auth whoami`                                           | none           | —                                                                                                                                        | Prints the effective principal, one field per line: `user_id`, `email`, `name`, `source` (`local\|oidc\|session`), `session` (path). |
| Auth    | `auth token mint`                                       | none           | `--ttl` (default `15m`)                                                                                                                  | **Stub.** Always fails (exit `1`) pending the tokens issuer.                                                                         |
| Tenancy | `org list`                                              | none           | —                                                                                                                                        | table `SLUG NAME CREATED`                                                                                                            |
| Tenancy | `org create --name <n> [--slug <s>]`                    | none           | `--name` required; `--slug` is generated when omitted                                                                                    | `Created org <slug> (<uuid>)`                                                                                                        |
| Tenancy | `project list`                                          | none           | —                                                                                                                                        | table `SLUG NAME CREATED`                                                                                                            |
| Tenancy | `project create --name <n> [--slug <s>]`                | none           | `--name` required; `--slug` is generated when omitted                                                                                    | `Created project <slug> (<uuid>)`                                                                                                    |
| Tenancy | `invite list`                                           | none           | —                                                                                                                                        | table `ID EMAIL ROLE STATUS EXPIRES`                                                                                                 |
| Tenancy | `invite send --email <a> [--role owner\|admin\|member]` | none           | `--email` required, `--role` defaults to `member`                                                                                        | `Invite queued: id=… email=… role=…`                                                                                                 |
| Tenancy | `invite revoke <id>`                                    | 1, uuid        | —                                                                                                                                        | `Revoked invite <uuid>`                                                                                                              |
| Meta    | `version`                                               | none           | —                                                                                                                                        | `openwa <v> (commit <c>, built <t>)`                                                                                                 |
| Meta    | `healthz`                                               | none           | `--timeout` (default `3s`)                                                                                                               | Probes `/healthz`; exits `0` on 2xx, `1` otherwise.                                                                                  |
| Meta    | `completion <bash\|zsh\|fish\|powershell>`              | 1, validated   | —                                                                                                                                        | Writes the completion script to stdout.                                                                                              |

- `init` creates the singleton org only in `selfhosted` mode (ignored in `cloud`). A blank
  `--org-slug` uses `tenant.singletonOrg.slug`, else a generated slug; an existing system org is reused.
- `serve` flag exclusions: `--no-scheduler`/`--scheduler-only`, `--no-consumer`/`--consumer-only`
  and `--consumer-only`/`--scheduler-only`. `--no-consumer` keeps full HTTP and stops consuming
  jobs; `--consumer-only` serves health probes only, consumes jobs and runs no scheduler, and fails
  boot with `queue.enabled=false`. `--scheduler-only` runs no consumer. What each combination
  runs: [`queue`](../queue/README.md#serve-flags).
- `scheduler run` exits `5` on an unknown job (`scheduler.unknown_job`), `6` when it is already
  running here or another replica holds the lock, and `7` when the runner is draining or
  `scheduler.enabled=false` (`scheduler.disabled`).
- `auth login --admin` is break-glass: it forces local login even when OIDC is configured, with a
  warning on stderr. `--email` + `--password-stdin` is the non-interactive local path; the password
  is read unmasked from stdin.
- `healthz` text output is `ok <url>  (<status>, <elapsed>)` or
  `fail <url>  (<status\|error>, <elapsed>)`. It is the same binary used as the compose and k8s
  healthcheck, so it needs no shell. `/healthz`, `/readyz` and `/robots.txt` are mounted on the
  **outer** mux at root under the `Probes` chain, never under `http.basePath` — probe URLs must not
  move when the app is remounted.

## Domain

### todo — control plane (S2)

Hidden from `--help`; kept as the reference for a domain command.

| Command               | Args                | Flags                                   | Prints                       |
| --------------------- | ------------------- | --------------------------------------- | ---------------------------- |
| `todo list`           | none                | `--done`, `--open` — mutually exclusive | table `ID DONE TITLE`        |
| `todo add <title...>` | 1+, joined by space | —                                       | `Added todo <uuid>: <title>` |
| `todo toggle <id>`    | 1, uuid             | —                                       | `<uuid> done=<bool>`         |
| `todo delete <id>`    | 1, uuid             | —                                       | `Deleted todo <uuid>`        |

Omit both filters to list everything. Filtering happens client-side after the RPC returns.

### device — control plane (S2)

The token's principal decides the org and the project. `--device` takes a name (case-insensitive,
resolved through `ListDevices`) or a device public id (`dev_…`). Every id the commands print is
the public id; the internal UUID never appears. A project key carries an active project; an org key or personal token does not, and the device
commands do not send `--project`. With such a credential, `pair` and `logout` without `--device`
are refused with `PRJ005` (exit `7`); pass `--device` or use a project key.

| Command         | Args | Flags                 | Prints                                                                            |
| --------------- | ---- | --------------------- | --------------------------------------------------------------------------------- |
| `device list`   | none | —                     | table `NAME STATE PHONE ID`                                                       |
| `device get`    | none | `--device` (required) | `id / name / state / phone / push name / version`, one per line                   |
| `device create` | none | `--name` (required)   | `Created device <name> (<dev_id>)`                                                |
| `device pair`   | none | `--device`, `--phone` | a terminal QR (or the pairing code) on stderr, then `Pairing <dev_id>: <outcome>` |
| `device logout` | none | `--device`            | `Logged out device <dev_id>`                                                      |
| `device delete` | none | `--device` (required) | `Deleted device <dev_id>`                                                         |

- `pair` and `logout` without `--device` act on the project's only device; with several devices
  (or none) the server refuses and lists them (exit `4`, `GEN004`). Server errors keep their code
  and map to exit codes as in the exit table.
- `pair` polls every 5 s and reprints the QR when WhatsApp rotates it; it exits `0` on
  `connected`, `1` on `timeout` or `failed` (in every output format), and on Ctrl-C stops with exit `1` (never `0`) so `pair && next` does not proceed unlinked.
  The QR and the pairing code always go to stderr, so `--output json` keeps stdout one envelope.

### send, chat, message, contact, group — control plane (S2)

Every command takes `--device <name|id>`; without it the server uses the project's only
device and refuses (exit `4`, `GEN004`) when there are several, naming them.

| Command         | Args            | Flags                                                                            | Prints                                            |
| --------------- | --------------- | -------------------------------------------------------------------------------- | ------------------------------------------------- |
| `send text`     | none            | `--to` (required), `--text` (required), `--reply-to`, `--mention`, `--mark-read` | `Queued message <msg_id> to <to> (status queued)` |
| `send image`    | none            | `--to`, `--file` or `--url`, `--caption`, and the `send text` extras             | same                                              |
| `send document` | none            | `--to`, `--file` or `--url`, `--filename`, `--caption`, `--mime`                 | same                                              |
| `send location` | none            | `--to`, `--lat`, `--lng` (required), `--name`, `--address`                       | same                                              |
| `chat list`     | none            | `--kind dm\|group`, `--q`, `--cursor`, `--limit`, `--all`                        | table `NAME KIND UNREAD LAST PREVIEW ID`          |
| `chat show`     | `<chat-id>`     | `--limit` (20)                                                                   | the chat, then table `WHEN FROM BODY STATUS ID`   |
| `message list`  | none            | `--chat` or `--device`, `--since`, `--cursor`, `--limit`, `--all`                | table `WHEN FROM BODY STATUS ID`                  |
| `contact list`  | none            | `--q`, `--cursor`, `--limit`, `--all`                                            | table `NAME PHONE JID DEVICE`                     |
| `group list`    | none            | none                                                                             | table `NAME MEMBERS JOINED JID CHAT ID`           |
| `group join`    | `<invite-link>` | none                                                                             | `Joined group <name> (<cht_id>)`                  |
| `group leave`   | `<chat-id>`     | none                                                                             | `Left group <jid>`                                |

- `--file` takes files up to the server's `whatsapp.mediaMaxBytes` (32 MiB by default); larger
  files go by `--url`. The 4 MiB inline cap applies only to MCP tool calls.
- List commands: `--output text` prints `more: --cursor <c>` on stderr when a next page
  exists; `--output json` prints `{"data":{"items":[…],"next_cursor":"…"}}`; `--output ndjson`
  prints one object per line. Single-record commands (`send`, `chat show`, `group join`,
  `group leave`) print json for both json and ndjson. `--all` follows the cursor to the last page
  and cannot be combined with `--cursor`.
- `send` returns once the message is queued. Delivery is asynchronous; watch
  `message.status` webhooks or `openwa message list --chat <cht_id>`.

## Payload fields

| Command                                       | Fields under `data`                                                                                                                                               |
| --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `scheduler list`                              | `name`, `scope`, `schedule`, `timeout`, `singleton`                                                                                                               |
| `auth whoami`                                 | `user_id`, `email`, `name`, `source`, `session_path`                                                                                                              |
| `org list`                                    | `id`, `slug`, `name`, `owner_id`, `created_at`                                                                                                                    |
| `project list`                                | `id`, `org_id`, `slug`, `name`, `created_at`                                                                                                                      |
| `invite list`                                 | `id`, `org_id`, `email`, `role`, `status`, `expires_at`, `created_at`                                                                                             |
| `todo list`                                   | `id`, `project_id`, `title`, `done`, `created_at`                                                                                                                 |
| `device list` / `get` / `create`              | `id`, `name`, `state`, `phone`, `push_name`, `version`, `last_seen_at`                                                                                            |
| `device pair`                                 | `device_id`, `outcome`                                                                                                                                            |
| `device logout`                               | `device_id`, `logged_out`                                                                                                                                         |
| `device delete`                               | `device_id`, `deleted`                                                                                                                                            |
| `send *`, `message list`                      | `id`, `device_id`, `chat_id`, `direction`, `wa_id`, `type`, `status`, `body`, `sender_phone`, `sender_name`, `from_me`, `error`, `timestamp`, `media`, `location` |
| `chat list` / `show` / `group join` / `leave` | `id`, `device_id`, `jid`, `kind`, `name`, `last_message_preview`, `unread_count`, `archived`, `last_message_at`                                                   |
| `contact list`                                | `device_id`, `jid`, `phone`, `name`, `push_name`, `business_name`, `display_name`, `updated_at`                                                                   |
| `group list`                                  | `jid`, `name`, `topic`, `participants`, `announce`, `locked`, `invite_link`, `chat_id`, `joined`                                                                  |
| `version`                                     | `version`, `commit`, `buildTime`                                                                                                                                  |
| `healthz`                                     | `url`, `status`, `ok`, `took`, `error`                                                                                                                            |

Timestamps are RFC 3339. `role` is `owner\|admin\|member`; `status` is `pending\|accepted` for an
invite.

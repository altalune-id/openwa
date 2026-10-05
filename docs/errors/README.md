# Error codes

Every user-visible failure carries a code, shown next to the request id so a report matches a log
line. Codes are `<DOM><NNN>`: a three-letter domain plus a sequence. **Append-only** — never
renumbered or reused, because users quote them. `900`-`999` is reserved for internal failures.

`TestCodes_EveryRefIsDocumented` pins this file to `internal/apperror/codes.go` both ways. Adding a
code: [`howto/error-code.md`](../howto/error-code.md); raising one: [`howto/errors.md`](../howto/errors.md).

## Where a code travels

- **Console (S1)** — on the error page and in the form banner. The handler picks the HTTP status.
- **Control plane `/api/` (S2)** — in `ErrorDetail.code` inside the Connect error. The Status
  column is the gRPC code the envelope carries; `interceptor/codes.go` maps it to Connect's.
- **MCP `/mcp` (S7)** — in the 401 body and in a failed tool's `ErrorPayload`. See [`mcp`](../mcp/README.md).
- **CLI (S6)** — printed with the message; exit codes are separate ([`cli`](../cli/README.md)).
- **Data plane `/api/v1/` (S3) and ingest `/hooks/` (S4) emit no codes** — only an opaque outcome
  word (`not_found`, `unauthorized`, `bad_request`, `conflict`, `in_progress`,
  `precondition_failed`, `precondition_required`, `method_not_allowed`, `payload_too_large`, `gone`,
  `internal`), so a denied scope and a missing row look the same. See [`surfaces`](../surfaces/README.md) R6.

In the Status column, `—` means registered but not yet constructed.

## GEN — General / cross-cutting

| Code     | Constant                       | Status             | Meaning          |
| -------- | ------------------------------ | ------------------ | ---------------- |
| `GEN001` | `apperror.CodeTenantMissing`   | `Unauthenticated`  | Tenant Missing   |
| `GEN002` | `apperror.CodeUnauthenticated` | `Unauthenticated`  | Unauthenticated  |
| `GEN003` | `apperror.CodeForbidden`       | `PermissionDenied` | Forbidden        |
| `GEN004` | `apperror.CodeValidation`      | `InvalidArgument`  | Validation       |
| `GEN005` | `apperror.CodeNotFound`        | `NotFound`         | Not Found        |
| `GEN006` | `apperror.CodeAlreadyExists`   | `AlreadyExists`    | Already Exists   |
| `GEN900` | `apperror.CodeUnexpectedError` | `Internal`         | Unexpected Error |

## USR — Users

| Code     | Constant                         | Status             | Meaning             |
| -------- | -------------------------------- | ------------------ | ------------------- |
| `USR001` | `apperror.CodeUserNotFound`      | `NotFound`         | User Not Found      |
| `USR002` | `apperror.CodeUserAlreadyExists` | `AlreadyExists`    | User Already Exists |
| `USR003` | `apperror.CodeUserNotInvited`    | `PermissionDenied` | User Not Invited    |
| `USR004` | `apperror.CodeUserInvalidEmail`  | `InvalidArgument`  | User Invalid Email  |
| `USR005` | `apperror.CodeUserInvalidName`   | `InvalidArgument`  | User Invalid Name   |

## ORG — Organizations

| Code     | Constant                            | Status               | Meaning                |
| -------- | ----------------------------------- | -------------------- | ---------------------- |
| `ORG001` | `apperror.CodeOrgNotFound`          | `NotFound`           | Org Not Found          |
| `ORG002` | `apperror.CodeOrgAlreadyExists`     | `AlreadyExists`      | Org Already Exists     |
| `ORG003` | `apperror.CodeOrgInvalidSlug`       | `InvalidArgument`    | Org Invalid Slug       |
| `ORG004` | `apperror.CodeOrgInvalidName`       | `InvalidArgument`    | Org Invalid Name       |
| `ORG005` | `apperror.CodeOrgMembershipExists`  | `AlreadyExists`      | Org Membership Exists  |
| `ORG006` | `apperror.CodeOrgMembershipMissing` | `NotFound`           | Org Membership Missing |
| `ORG007` | `apperror.CodeOrgCreationDisabled`  | `FailedPrecondition` | Org Creation Disabled  |
| `ORG008` | `apperror.CodeOrgSystemProtected`   | `FailedPrecondition` | Org System Protected   |
| `ORG009` | `apperror.CodeOrgSelfRemoval`       | `FailedPrecondition` | Org Self Removal       |
| `ORG010` | `apperror.CodeOrgOwnerRemoval`      | `FailedPrecondition` | Org Owner Removal      |
| `ORG011` | `apperror.CodeOrgManagerRequired`   | `PermissionDenied`   | Org Manager Required   |

## PRJ — Projects

| Code     | Constant                              | Status               | Meaning                  |
| -------- | ------------------------------------- | -------------------- | ------------------------ |
| `PRJ001` | `apperror.CodeProjectNotFound`        | `NotFound`           | Project Not Found        |
| `PRJ002` | `apperror.CodeProjectAlreadyExists`   | `AlreadyExists`      | Project Already Exists   |
| `PRJ003` | `apperror.CodeProjectInvalidSlug`     | `InvalidArgument`    | Project Invalid Slug     |
| `PRJ004` | `apperror.CodeProjectSystemProtected` | `FailedPrecondition` | Project System Protected |
| `PRJ005` | `apperror.CodeProjectUnresolved`      | `FailedPrecondition` | Project Unresolved       |

## INV — Invites

| Code     | Constant                         | Status               | Meaning             |
| -------- | -------------------------------- | -------------------- | ------------------- |
| `INV001` | `apperror.CodeInviteNotFound`    | `NotFound`           | Invite Not Found    |
| `INV002` | `apperror.CodeInviteExpired`     | `FailedPrecondition` | Invite Expired      |
| `INV003` | `apperror.CodeInviteAlreadyUsed` | `FailedPrecondition` | Invite Already Used |
| `INV004` | `apperror.CodeInviteInvalidRole` | `InvalidArgument`    | Invite Invalid Role |
| `INV005` | `apperror.CodeInviteDisabled`    | `FailedPrecondition` | Invite Disabled     |

## TDO — Todos

| Code     | Constant                          | Status            | Meaning              |
| -------- | --------------------------------- | ----------------- | -------------------- |
| `TDO001` | `apperror.CodeTodoNotFound`       | `NotFound`        | Todo Not Found       |
| `TDO002` | `apperror.CodeTodoInvalidTitle`   | `InvalidArgument` | Todo Invalid Title   |
| `TDO003` | `apperror.CodeTodoAlreadyDeleted` | —                 | Todo Already Deleted |

## SGN — Signup

| Code     | Constant                      | Status               | Meaning         |
| -------- | ----------------------------- | -------------------- | --------------- |
| `SGN001` | `apperror.CodeSignupRequired` | `FailedPrecondition` | Signup Required |

## AUT — Authentication

| Code     | Constant                              | Status               | Meaning                  |
| -------- | ------------------------------------- | -------------------- | ------------------------ |
| `AUT001` | `apperror.CodeAuthInvalidCredentials` | `Unauthenticated`    | Auth Invalid Credentials |
| `AUT002` | `apperror.CodeAuthOIDCUnavailable`    | `FailedPrecondition` | Auth OIDC Unavailable    |
| `AUT003` | `apperror.CodeAuthOIDCClaimMissing`   | `InvalidArgument`    | Auth OIDC Claim Missing  |

## TKN — Tokens

| Code     | Constant                    | Status            | Meaning       |
| -------- | --------------------------- | ----------------- | ------------- |
| `TKN001` | `apperror.CodeTokenExpired` | `Unauthenticated` | Token Expired |

## ONB — Onboarding

| Code     | Constant                             | Status               | Meaning                 |
| -------- | ------------------------------------ | -------------------- | ----------------------- |
| `ONB001` | `apperror.CodeOnboardingRequired`    | `FailedPrecondition` | Onboarding Required     |
| `ONB002` | `apperror.CodeOnboardingAlreadyDone` | `AlreadyExists`      | Onboarding Already Done |

## ENC — Encryption at rest

| Code     | Constant                             | Status               | Meaning                |
| -------- | ------------------------------------ | -------------------- | ---------------------- |
| `ENC001` | `apperror.CodeEncryptionUnavailable` | `FailedPrecondition` | Encryption Unavailable |
| `ENC002` | `apperror.CodeEncryptionOpenFailed`  | `FailedPrecondition` | Encryption Open Failed |

## BLG — Blog posts

| Code     | Constant                            | Status               | Meaning                |
| -------- | ----------------------------------- | -------------------- | ---------------------- |
| `BLG001` | `apperror.CodePostNotFound`         | `NotFound`           | Post Not Found         |
| `BLG002` | `apperror.CodePostAlreadyExists`    | `AlreadyExists`      | Post Already Exists    |
| `BLG003` | `apperror.CodePostInvalidTitle`     | `InvalidArgument`    | Post Invalid Title     |
| `BLG004` | `apperror.CodePostInvalidSlug`      | `InvalidArgument`    | Post Invalid Slug      |
| `BLG005` | `apperror.CodePostInvalidBody`      | `InvalidArgument`    | Post Invalid Body      |
| `BLG006` | `apperror.CodePostCategoryRequired` | `InvalidArgument`    | Post Category Required |
| `BLG007` | `apperror.CodePostStaleVersion`     | `FailedPrecondition` | Post Stale Version     |

`BLG007` is the optimistic-concurrency outcome: `blog.StaleVersionError`, raised when a conditional
write's expected version no longer matches. On the data plane the same failure answers
`412 Precondition Failed`, and a write that omits the precondition answers `428`.

## CAT — Blog categories

| Code     | Constant                             | Status               | Meaning                 |
| -------- | ------------------------------------ | -------------------- | ----------------------- |
| `CAT001` | `apperror.CodeCategoryNotFound`      | `NotFound`           | Category Not Found      |
| `CAT002` | `apperror.CodeCategoryAlreadyExists` | `AlreadyExists`      | Category Already Exists |
| `CAT003` | `apperror.CodeCategoryInvalidName`   | `InvalidArgument`    | Category Invalid Name   |
| `CAT004` | `apperror.CodeCategoryInUse`         | `FailedPrecondition` | Category In Use         |

## TAG — Blog tags

| Code     | Constant                        | Status               | Meaning            |
| -------- | ------------------------------- | -------------------- | ------------------ |
| `TAG001` | `apperror.CodeTagNotFound`      | `NotFound`           | Tag Not Found      |
| `TAG002` | `apperror.CodeTagAlreadyExists` | `AlreadyExists`      | Tag Already Exists |
| `TAG003` | `apperror.CodeTagInvalidName`   | `InvalidArgument`    | Tag Invalid Name   |
| `TAG004` | `apperror.CodeTagInUse`         | `FailedPrecondition` | Tag In Use         |

## MCP — Model Context Protocol surface (S7)

| Code     | Constant                          | Status            | Meaning         |
| -------- | --------------------------------- | ----------------- | --------------- |
| `MCP001` | `apperror.CodeMCPUnauthenticated` | `401` (HTTP, raw) | Unauthenticated |

`MCP001` is written by the MCP transport itself, before any tool runs: a bare `401` with
`WWW-Authenticate` and an `mcp.ErrorPayload` body. A failure inside a tool call goes through
`mcp.TranslateError` instead and carries `GEN002` for a rejected credential, and `GEN003` — the
scope-denial code, returned by `internal/mcp/auth.go` — for a denied or undeclared scope.
Tool arguments that do not decode into the tool's input message (a wrong JSON type, or an
unknown field under the strict decode) raise `mcp.InvalidArgumentsError` and answer `GEN004`
with `meta.tool` and, when the decoder names it, `meta.field`. It is a client error, not an
unmapped `GEN900` incident.

## APK — API keys

| Code     | Constant                                | Status               | Meaning                      |
| -------- | --------------------------------------- | -------------------- | ---------------------------- |
| `APK001` | `apperror.CodeAPIKeyUnknownScope`       | `InvalidArgument`    | API Key Unknown Scope        |
| `APK002` | `apperror.CodeAPIKeyRetiredScope`       | `InvalidArgument`    | API Key Retired Scope        |
| `APK003` | `apperror.CodeAPIKeyScopeLevel`         | `InvalidArgument`    | API Key Scope Level          |
| `APK004` | `apperror.CodeAPIKeyEmptyGrant`         | `InvalidArgument`    | API Key Empty Grant          |
| `APK005` | `apperror.CodeAPIKeyGrantConflict`      | `InvalidArgument`    | API Key Grant Conflict       |
| `APK006` | `apperror.CodeAPIKeyBoundToProject`     | `FailedPrecondition` | API Key Bound To Project     |
| `APK007` | `apperror.CodeAPIKeyAlreadyAllProjects` | `FailedPrecondition` | API Key Already All Projects |
| `APK008` | `apperror.CodeAPIKeyRevoked`            | `FailedPrecondition` | API Key Revoked              |
| `APK009` | `apperror.CodeAPIKeyProjectNotInOrg`    | `InvalidArgument`    | API Key Project Not In Org   |
| `APK010` | `apperror.CodeAPIKeyExpiryRequired`     | `InvalidArgument`    | API Key Expiry Required      |
| `APK011` | `apperror.CodeAPIKeyExpiryInPast`       | `InvalidArgument`    | API Key Expiry In Past       |
| `APK012` | `apperror.CodeAPIKeyExpiryTooLong`      | `InvalidArgument`    | API Key Expiry Too Long      |
| `APK013` | `apperror.CodeAPIKeyInvalidResource`    | `InvalidArgument`    | API Key Invalid Resource     |
| `APK014` | `apperror.CodeAPIKeyDeviceBindingScope` | `InvalidArgument`    | API Key Device Binding Scope |

## WHK — Webhooks

| Code     | Constant                                   | Status               | Meaning                        |
| -------- | ------------------------------------------ | -------------------- | ------------------------------ |
| `WHK001` | `apperror.CodeWebhookEndpointNotFound`     | `NotFound`           | Webhook Endpoint Not Found     |
| `WHK002` | `apperror.CodeWebhookInvalidURL`           | `InvalidArgument`    | Webhook Invalid URL            |
| `WHK003` | `apperror.CodeWebhookInvalidEventTypes`    | `InvalidArgument`    | Webhook Invalid Event Types    |
| `WHK004` | `apperror.CodeWebhookEndpointLimit`        | `FailedPrecondition` | Webhook Endpoint Limit         |
| `WHK005` | `apperror.CodeWebhookDeliveryNotRetryable` | `FailedPrecondition` | Webhook Delivery Not Retryable |
| `WHK006` | `apperror.CodeWebhookDeliveryNotFound`     | `NotFound`           | Webhook Delivery Not Found     |
| `WHK007` | `apperror.CodeWebhookEndpointInactive`     | `FailedPrecondition` | Webhook Endpoint Inactive      |
| `WHK008` | `apperror.CodeWebhookSecretConflict`       | `FailedPrecondition` | Webhook Secret Conflict        |

## DEV — Devices

| Code     | Constant                          | Status            | Meaning              |
| -------- | --------------------------------- | ----------------- | -------------------- |
| `DEV001` | `apperror.CodeDeviceNotFound`     | `NotFound`        | Device Not Found     |
| `DEV002` | `apperror.CodeDeviceNameTaken`    | `AlreadyExists`   | Device Name Taken    |
| `DEV003` | `apperror.CodeDeviceInvalidName`  | `InvalidArgument` | Device Invalid Name  |
| `DEV004` | `apperror.CodeDeviceInvalidRules` | `InvalidArgument` | Device Invalid Rules |
| `DEV005` | `apperror.CodeDeviceStaleVersion` | `Aborted`         | Device Stale Version |

`DEV005` is the optimistic-concurrency outcome of `device.StaleVersionError`. On the data plane it answers `412 Precondition Failed`.

## WAS — WhatsApp sessions

| Code     | Constant                               | Status               | Meaning                                    |
| -------- | -------------------------------------- | -------------------- | ------------------------------------------ |
| `WAS001` | `apperror.CodeWhatsAppSessionNotFound` | `NotFound`           | WhatsApp Session Not Found                 |
| `WAS002` | `apperror.CodeWhatsAppNotOwned`        | `Unavailable`        | WhatsApp Session Not Owned By This Process |
| `WAS003` | `apperror.CodeWhatsAppAlreadyLinked`   | `FailedPrecondition` | WhatsApp Session Already Linked            |
| `WAS004` | `apperror.CodeWhatsAppLinkTimeout`     | `DeadlineExceeded`   | WhatsApp Link Timeout                      |
| `WAS005` | `apperror.CodeWhatsAppUnsupported`     | `Unimplemented`      | WhatsApp Feature Unsupported By The Engine |
| `WAS006` | `apperror.CodeWhatsAppNotConnected`    | `FailedPrecondition` | WhatsApp Session Not Connected             |
| `WAS007` | `apperror.CodeWhatsAppInvalidPhone`    | `InvalidArgument`    | WhatsApp Invalid Phone Number              |
| `WAS008` | `apperror.CodeWhatsAppSessionGone`     | `NotFound`           | WhatsApp Session Gone From The Engine      |
| `WAS009` | `apperror.CodeWhatsAppEngine`          | `Internal`           | WhatsApp Engine Failure                    |

## MSG — Messages

| Code     | Constant                               | Status               | Meaning                    |
| -------- | -------------------------------------- | -------------------- | -------------------------- |
| `MSG001` | `apperror.CodeMessageNotFound`         | `NotFound`           | Message Not Found          |
| `MSG002` | `apperror.CodeMessageInvalidInput`     | `InvalidArgument`    | Message Invalid Input      |
| `MSG003` | `apperror.CodeMessageDeviceNotLinked`  | `FailedPrecondition` | Message Device Not Linked  |
| `MSG004` | `apperror.CodeMessageMediaTooLarge`    | `InvalidArgument`    | Message Media Too Large    |
| `MSG005` | `apperror.CodeMessageUnsupportedMime`  | `InvalidArgument`    | Message Unsupported Mime   |
| `MSG006` | `apperror.CodeMessageEditWindowClosed` | `FailedPrecondition` | Message Edit Window Closed |
| `MSG007` | `apperror.CodeMessageNotOwn`           | `FailedPrecondition` | Message Not Own            |
| `MSG008` | `apperror.CodeMessageVersionMismatch`  | `Aborted`            | Message Version Mismatch   |
| `MSG009` | `apperror.CodeMessageMediaUnavailable` | `FailedPrecondition` | Message Media Unavailable  |
| `MSG010` | `apperror.CodeMessageInvalidRetention` | `InvalidArgument`    | Message Invalid Retention  |
| `MSG011` | `apperror.CodeMessageMediaFetch`       | `InvalidArgument`    | Message Media Fetch        |

`MSG009` answers `410 Gone` on the data plane and the console media door: WhatsApp's CDN no longer holds the file. This applies to sent and received media alike.

## CHT — Chats

| Code     | Constant                           | Status               | Meaning               |
| -------- | ---------------------------------- | -------------------- | --------------------- |
| `CHT001` | `apperror.CodeChatNotFound`        | `NotFound`           | Chat Not Found        |
| `CHT002` | `apperror.CodeChatVersionMismatch` | `Aborted`            | Chat Version Mismatch |
| `CHT003` | `apperror.CodeChatInvalidJID`      | `InvalidArgument`    | Chat Invalid JID      |
| `CHT004` | `apperror.CodeChatNotAGroup`       | `FailedPrecondition` | Chat Not A Group      |

## CNT — Contacts

| Code     | Constant                       | Status     | Meaning           |
| -------- | ------------------------------ | ---------- | ----------------- |
| `CNT001` | `apperror.CodeContactNotFound` | `NotFound` | Contact Not Found |

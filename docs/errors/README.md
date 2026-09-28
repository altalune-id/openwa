# Error codes

Every user-visible failure carries a code, printed next to the request id so a report can be matched
to a log line. Codes are `<DOM><NNN>` — a three-letter domain mnemonic plus a per-domain sequence.
They are **append-only**: never renumbered, never reused after retirement, because users quote them
from screenshots. `NNN` in the range `900`-`999` is reserved for unexpected or internal failures.

`TestCodes_EveryRefIsDocumented` pins this file to `internal/apperror/codes.go` in both directions:
a constant with no row fails, and a row with no constant fails. Adding a code:
[`howto/error-code.md`](../howto/error-code.md); raising one: [`howto/errors.md`](../howto/errors.md).

## Where a code travels

- **Console (S1)** — rendered on the error page and in the inline form banner. The HTTP status is
  chosen by the handler, not by the code.
- **Control plane `/api/` (S2)** — the code rides in `ErrorDetail.code` inside the Connect error.
  The Status column below is the `google.golang.org/grpc/codes` value the envelope carries;
  `internal/controlplane/interceptor/codes.go` maps it to the `connect.Code` of the same name, and
  Connect derives the HTTP status from that.
- **MCP `/mcp` (S7)** — the transport's 401 body and a failed tool's `ErrorPayload` both carry a
  code. See [`mcp`](../mcp/README.md).
- **CLI (S6)** — printed with the message. The process exit code is a separate contract; see
  [`cli`](../cli/README.md).
- **Data plane `/api/v1/` (S3) and ingest `/hooks/` (S4) do not emit these codes.** Their JSON body
  is `{"code","message"}` where `code` is an outcome word — `not_found`, `unauthorized`,
  `bad_request`, `conflict`, `in_progress`, `precondition_failed`, `precondition_required`,
  `method_not_allowed`, `payload_too_large`, `internal`. That vocabulary is deliberately opaque, so a
  denied scope and a missing row are indistinguishable. See [`surfaces`](../surfaces/README.md) R6.

In the Status column, `—` means the code is registered but nothing constructs it yet.

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

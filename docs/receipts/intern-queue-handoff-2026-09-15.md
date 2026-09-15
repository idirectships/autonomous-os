# Intern canonical queue handoff — 2026-09-15

Origin: implementation-advisor@dru, Astra tool attribute.

## Scope and boundary

Worktree: `/Users/dru/DEV/autonomous-os-intern-queue-handoff-20260915`.
Branch: `codex/intern-queue-handoff-20260915`.
Base: `21c9ff3db`, the supplied runtime checkout's clean current branch,
`codex/gus-runtime-contract-20260914` (tracking `intern-pr-fork` at inspection).
The review targets that branch in `idirectships/autonomous-os`, isolating this
slice from the existing runtime implementation.

All project edits are confined to this new worktree. No live checkout edits,
credential file reads, SSH, authority/model/service calls, provisioning, merge,
or deployment occurred. GitHub metadata, push and PR creation are the explicitly
requested publication operations. Authority traffic in tests uses an injected
fake RoundTripper at the pinned URL, with synthetic tokens only. The inherited
service integration test uses a local fake bridge HTTP server; the dispatcher
and redirect test never contact a listener. The repository's broader test suite
also uses its existing local fixtures. Go dependency downloads were disabled.
No global Beads/substrate writes were made outside the authorized worktree.

## Contract evidence and prerequisite

Read-only Git-object inspection of the dotfiles cached `origin/main` at
`304f37b557d73587fcc7216474fccb5f2560c2ba` established:

- `.autonomous/scripts/gus_authority.py`: `_validated_bus_request`,
  `_validated_bus_task_payload`, `_handle_bus`, `post_task`, request fingerprint
  and replay handling. The canonical endpoint is `/messages/post-task`; common
  request keys are `request_id`, `role`, `node`, `requested_at`, `to_role`, `task`.
- `.autonomous/scripts/gus_comms_contract.py`: `CONTRACT_VERSION= gus.comms/v1`.
- The exact task keys are `schema_version`, `task_id`, `data_zone`,
  `custody_policy`, `instruction_inert`, `body`, with schema `gus-bus-task/v1`.

That inspected authority revision does **not** yet admit `intern@gus` to
`mcavoy@lab`/`pam@gus`, define those destinations' task policy, or return the full
requested enqueue acknowledgement. This client stages `business/business-only`
custody for both admitted public and business inputs and the four body keys
listed below. The companion authority change must validate those exact choices
and emit the strict acknowledgement before activation. Neither authority
compatibility nor downstream execution is claimed by these fake tests.
No change to `/dispatch` or authority-side validation is included.

## Implementation

- Dedicated `intern@gus` principal only; exact `CanonicalAuthorityURL` remains
  the default. Reject alternate paths/hosts/schemes/principals even when supplied
  in otherwise disabled constructor configuration. Missing token source keeps
  the empty default configuration disabled.
- Deterministic single-intent admission: news/headlines/briefing to McAvoy;
  notification/notify, alarm, reminder/remind to PAM. The admitted label must
  agree with the bridge proposal (generic `service` is allowed for a validated
  PAM intent). Mixed/unknown intents and explicit persona/peer/fan-out text fail
  closed. Existing trusted classification and home-control checks are retained.
- Queue body has only `service_intent`, `destination`, original `request_text`,
  and `idempotency_key=intern-<SHA256 of original OS run ID>`. Whole request is
  bounded to 16 KiB; no model output, history, thinking, execution parameters,
  persona memory or credentials are copied. Original run IDs must also meet
  the authority's alphanumeric-first identifier requirement.
- Replay map stores only fingerprints and original Unix-second timestamps,
  protected by a mutex and capped at 4096 entries with no eviction. Identical
  explicit calls preserve the complete envelope; conflicts fail before network
  I/O. One transport attempt per call, no automatic retries. Authority owns
  durable deduplication, stale rejection and restart reconciliation. Fresh
  dispatcher construction is not an authorization to retry uncertain work.
- Strict JSON acknowledgement: `ok=true`, `contract_version=gus.comms/v1`,
  bounded opaque `message_id`, matching destination and `task_id` and/or
  `request_id` (both checked when present), status `accepted|queued`, optional
  `delivered=false`. Reject unknown fields, nested/duplicate/trailing JSON,
  malformed encodings, wrong correlations and synchronous completion.
- `ServiceDispatcher`, `DispatchConfig`, `DispatchReceipt` and callers retain
  their public shape. Message ID is validated then discarded; no authority
  body is exposed. Receipt always has `delivered=false`.
- Existing fixed error taxonomy, cancellation behavior, 20-second timeouts,
  response size/header/content-type/encoding limits, redirect refusal and bearer
  validation remain. Transport/HTTP/ack failures remain `ErrDispatch` with remote
  outcome unknown; pre-canceled requests remain `ErrCanceled`.

## Runtime Proof

Commands ran in the worktree above. `GOPROXY=off GOSUMDB=off` was supplied for all
Go test/vet commands; the table includes those exact prefixes.

| Command | Observed result |
| --- | --- |
| `git worktree add -b codex/intern-queue-handoff-20260915 /Users/dru/DEV/autonomous-os-intern-queue-handoff-20260915 21c9ff3db` (from supplied checkout) | Exit 0; new clean branch/worktree at the requested base. |
| `GOPROXY=off GOSUMDB=off go test ./runtimes/intern -run 'TestDispatch\|TestServiceDispatch' -count=1` | Exit 0; Intern package passed. |
| `GOPROXY=off GOSUMDB=off go test ./... -count=1` | Exit 0; all root-module packages passed or had no tests. |
| `GOPROXY=off GOSUMDB=off go test -race ./... -count=1` | Exit 0; full root-module race suite passed, no race reports. |
| `GOPROXY=off GOSUMDB=off go vet ./...` | Exit 0; no diagnostics. |
| `GOPROXY=off GOSUMDB=off go test -race ./runtimes/intern -run 'TestDispatch\|TestServiceDispatch' -count=1` | Exit 0; final added concurrency, timestamp-boundary replay and oversized-envelope cases passed (`2.367s`). |
| `GOPROXY=off GOSUMDB=off go vet ./runtimes/intern` | Exit 0; no diagnostics. |
| `gofmt -w runtimes/intern/dispatch.go runtimes/intern/dispatch_test.go` | Exit 0. |
| `gofmt -l runtimes/intern/dispatch.go runtimes/intern/dispatch_test.go` | Exit 0, empty output. |
| `git diff --check` | Exit 0, empty output. |

Initial tests exposed one incorrect negative fixture: `message_id="wrong"`
is a valid opaque ID. That fixture expectation was corrected, with no relaxation
of the parser; full tests/race/vet subsequently passed. Final additional tests
were verified with the focused race command above. Positive and negative cases
run together, covering both destinations, intent mapping, config/path/principal,
exact payload, input/envelope bounds, missing/invalid/removed bearer tokens,
custody/correlation rejection, acknowledgement fields/types/statuses, redirects,
response limits, cancellation, errors without secret echo, replay conflicts,
concurrent ambiguous attempts and reservation capacity.

## Changed files

- `runtimes/intern/dispatch.go`
- `runtimes/intern/dispatch_test.go`
- `docs/agentic/intern-bridge-client.md`
- `docs/vi/agentic/intern-bridge-client_vi.md`
- `docs/receipts/intern-queue-handoff-2026-09-15.md`

The earlier service-dispatch receipt is historical; this receipt and the updated
English/Vietnamese contract supersede its `/dispatch` transport description.
Provider billing/token usage was not available from these local proof commands;
no zero-usage claim is made.

# BLOCKED: Intern canonical service enqueue contract

Origin: implementation-advisor@dru, Astra tool attribute. Date: 2026-09-15.

## Decision

Stopped at the Director's checkpoint. A verified implementation PR cannot be
established against the available authority contract. The proposed client and
documentation changes have been withdrawn; the four existing files are restored
exactly to OS base `21c9ff3db`. The branch's net change is this blocker receipt.
The speculative implementation commit remains only in Git history, followed by
the withdrawal commit; history was not rewritten. PR
`https://github.com/idirectships/autonomous-os/pull/1` is withdrawn, not a verified
implementation deliverable.

## Exact available source

OS worktree: `/Users/dru/DEV/autonomous-os-intern-queue-handoff-20260915`.
Branch: `codex/intern-queue-handoff-20260915`.
Base branch: `codex/gus-runtime-contract-20260914`, commit `21c9ff3db`.

Read-only dotfiles Git-object inspection used cached `origin/main` and
`codex/intern-authority-admission-20260915`. Both resolved to
`304f37b557d73587fcc7216474fccb5f2560c2ba` at the checkpoint. No fetch, credentials,
SSH or live authority probe was used to infer compatibility.

The following common contract is available:

- `.autonomous/scripts/gus_comms_contract.py`: `CONTRACT_VERSION="gus.comms/v1"`.
- `.autonomous/scripts/gus_authority.py`, `_handle_bus`: canonical
  `POST /messages/post-task`; exact common fields `request_id`, `role`, `node`,
  `requested_at` (integer Unix seconds), `to_role`, `task`.
- `_validated_bus_task_payload`: exact task fields `schema_version`, `task_id`,
  `data_zone`, `custody_policy`, `instruction_inert`, `body`; schema
  `gus-bus-task/v1`, inert flag true, dictionary body.
- `post_task`: returns `message_id` and `idempotent`; the handler wraps those
  with `ok` and common contract metadata. It does not establish the requested
  full service enqueue acknowledgement.

## Exact missing contract

1. **Producer authorization:** an authoritative `intern@gus` registration/ACL
   for `POST /messages/post-task`, restricted to `mcavoy@lab` and `pam@gus`.
   The inspected `BUS_PRODUCER_DESTINATIONS` only registers
   `orchestration@gus -> cassi@mama|codex@lab`.
2. **Destination task policies:** authoritative `data_zone` and `custody_policy`
   for McAvoy and PAM. `BUS_TASK_POLICY` contains orchestration, Cassi and Codex,
   but neither requested service destination. Choosing `business/business-only`
   for the absent destinations would be speculative.
3. **Service body schema:** authority-owned exact key names, intent allowlist,
   destination/intent mapping, original-text bounds, idempotency key semantics
   and replay behavior for the two services. The common bus only requires a
   nonempty dictionary; it does not define the requested service admission body.
4. **Enqueue acknowledgement schema and fixtures:** authoritative values/types
   and allowed keys for `ok=true`, `contract_version`, `message_id`, correlated
   `task_id` and/or `request_id`, `status=accepted|queued`, `destination`, and
   optional `delivered=false`. The service-specific correlation, status and
   destination fields are missing from the inspected queue handler. Treatment
   of existing `idempotent` and common metadata must also be specified.

Unblock with a committed authority contract and positive/negative offline
fixtures covering those four items. Resume only against that exact revision;
do not substitute `/dispatch`, another principal, weaker custody or live probes.

## Proof and cleanup

Exact inspection commands (from the OS worktree):

```sh
git --git-dir=/Users/dru/.git rev-parse codex/intern-authority-admission-20260915 origin/main
git --git-dir=/Users/dru/.git show codex/intern-authority-admission-20260915:.autonomous/scripts/gus_authority.py | rg -n -A 10 'BUS_TASK_POLICY =|BUS_PRODUCER_DESTINATIONS =|return \{"message_id": message_id, "idempotent"'
```

Observed: both refs resolve to the commit above; task policies at lines 541–545
omit McAvoy/PAM; producer destinations at 551–553 omit Intern; queue returns at
1927 and 2027 carry only `message_id` and `idempotent`.

The withdrawn implementation passed focused Go tests, full `go test ./...`, full
`go test -race ./...`, `go vet ./...`, gofmt and diff checks. All Go checks used
`GOPROXY=off GOSUMDB=off`. Those tests verified a proposed fake contract only;
they are **not authority compatibility proof** and do not justify activation.

Restoration verification:

```sh
git diff --exit-code 21c9ff3db -- runtimes/intern/dispatch.go runtimes/intern/dispatch_test.go docs/agentic/intern-bridge-client.md docs/vi/agentic/intern-bridge-client_vi.md
git diff --check
```

Required observed result: exit 0 and no diff for the four existing files; net
branch diff contains only this blocker receipt. The final task response records
the withdrawal commit and confirmed PR state.

## No-live-action boundary

No changes to `/Users/dru` live checkout, credentials, SSH, live GUS/model/service
calls, merge or deployment. Edits and restoration are confined to the new OS
worktree. Dispatcher tests used fake HTTP transports and synthetic tokens; the
inherited integration tests used their local fake bridge server. GitHub was used
only for requested PR publication and its withdrawal. No external memo or Beads
mutation was made outside the authorized workspace. Provider billing was not
available; no zero-usage claim is made.

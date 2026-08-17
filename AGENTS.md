# AGENTS.md

## Build and test

Use the Go toolchain on `PATH` (on the current macOS workstation it is `/opt/homebrew/bin/go`).

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Run the same four checks in `things-cloud-sdk/` after SDK changes. The server defaults to port `8080`.

Environment variables:

- `PORT`: HTTP port.
- `DATA_DIR`: SQLite and credential-key directory (default `data/`).
- `JWT_SECRET`: base64 JWT signing key; generated when absent.
- `CREDENTIALS_SECRET`: durable high-entropy secret used to derive the AES key for Things passwords at rest. When absent, the server creates `DATA_DIR/credentials.key` with mode `0600`.
- `THINGS_DEBUG`: SDK debug logging.

## Architecture and safety invariants

The single-package Go MCP server is in `main.go`, `oauth.go`, and `landing.go`. Requests authenticate with Basic credentials or OAuth bearer tokens. `UserManager` creates a per-account `ThingsMCP`; the cache binds an account to a digest of the authenticated credentials, never just an email address.

Key SDK types from `github.com/arthursoares/things-cloud-sdk`:

- `Task.CreationDate` is `time.Time`; `ScheduledDate`, `DeadlineDate`, and `CompletionDate` are pointers.
- `TaskType`: `0` task, `1` project, `2` heading. `TaskStatus`: `0` pending, `2` canceled, `3` completed.
- `TaskSchedule`: `0` inbox, `1` anytime, `2` someday. `Task.StartBucket`: `0` default, `1` tonight (wire field `sb`).

Every operation for one account holds that account's `opMu` for the complete sync, validation, handler, and write lifecycle. Do not narrow this lock: history cursors and the in-memory task graph must advance atomically.

Synchronization is fail-closed:

- Resolve and use the history key returned by `Verify`; never select the numerically largest history.
- Initial load builds a candidate history and state, then swaps both only after every page and event validates.
- Incremental sync uses a cloned cursor and applies a fully validated delta atomically.
- Never automatically fall back to a full rebuild after an incremental error.
- Reject unknown schema versions, business-entity kinds, actions, malformed payloads, cursor regression, and no-progress pagination. Versioned `Settings<digits>` records are the explicit exception: they are account metadata and must be ignored so settings-only version bumps cannot block the task graph.

Writes continue to use the unofficial Things Cloud endpoint so the server remains remote and multi-user. Preserve these safeguards:

- Validate all item envelopes, UUIDs, relationships, dates, recurrence, and destructive confirmations before POST.
- Active task lists inherit container state: a pending child of a completed, canceled, or trashed heading/project is not active and must not appear in default, Today, Upcoming, overview, or diagnostic active counts. Direct show/project-detail tools may still expose the child's raw status for inspection.
- Creating or moving a task/heading requires every destination heading/project in its container chain to be pending and outside Trash; reject inactive destinations before POST.
- A transport failure or malformed response after POST is an uncertain commit. Reconcile by reading the authoritative history; never retry that POST automatically.
- After a confirmed commit, apply the exact validated events and server head locally so a successful write cannot be reported as a false failure.
- Recurring creation is a single atomic commit containing a recurrence template and its visible instance.
- Permanent area, tag, and checklist deletion requires `confirm=true`; stopping recurrence requires `confirm_destructive=true`.

OAuth persistence encrypts Things passwords with AES-GCM and stores refresh-token hashes only. Startup migrates legacy plaintext rows. Never log credentials, response bodies that may contain credentials, encryption keys, or bearer tokens. If encrypted rows exist but the key is missing, startup must fail rather than generate an unrecoverable replacement.

## MCP tools

There are 23 tools. All tool definitions are registered in `main.go`. Every tool must declare `RawOutputSchema`, return schema-conforming `structuredContent` in the `{ "data": ... }` envelope, and keep a JSON text fallback for older clients. Keep tool descriptions, parameters, destructive/read-only annotations, `landing.go`, and this file synchronized.

Before modifying tool definitions, review the current MCP builder guidance for naming, parameter descriptions, enums, output structure, and behavioral annotations. Handlers follow `func (t *ThingsMCP) handle<Name>(ctx, req) (*mcp.CallToolResult, error)` and are registered through `wrap()`. `things_diagnose` is the exception because it creates a fresh read-only diagnostic client from the request credentials.

Use `errResult(msg)` for tool errors and `jsonResult(v)` for successful structured results. Validate referenced UUIDs and options before constructing a write. Diagnostic metadata lives in `diagStepDefs`; `addSkippedSteps` records later steps after a failure, and diagnostic JSON uses camelCase keys.

## Wire format

Writes use abbreviated fields such as `tt`, `nt`, `st`, `dd`, and `sb`. Notes require CRC32 metadata. Date-only schedule and deadline fields are timezone-agnostic. Recurrence uses template-plus-instance entities: templates hold `rr` and `icsd`; visible instances hold `rt` referencing the template. Recurrence templates must remain hidden from normal read-tool results.

`parseDate()` accepts RFC3339 first and then `YYYY-MM-DD`. Date filters are exclusive. Output dates use ISO 8601 and omit zero-value years. User recurrence strings such as `daily`, `weekly:mon,wed`, `monthly:15`, and `every 3 days` are converted to the Things wire representation; weekly rules use the `wd` bitmask.

## Live validation and deployment

Do not use a deployed MCP tool to test uncommitted local code. Prefer the fake Things Cloud integration server in the test suite. Production checks must be read-only unless the user explicitly authorizes a write to a disposable account.

Before any deployment that migrates OAuth data:

1. Back up `oauth.db` together with its WAL/SHM files while the service is stopped, or use SQLite's online backup mechanism.
2. Confirm the credential key is durable and has mode `0600`.
3. Cross-compile, copy the binary, restart the user service, and verify health plus a read-only MCP call.
4. Retain the database backup and matching credential key for rollback.

Use `THINGS_BASIC_AUTH` for local curl examples; never place an email/password or encoded credential in source files, shell history, or logs.

The production service runs as the `wenbo` user service `things-mcp` on `wenbo@e.wenbo.io`, with its binary and working directory under `/home/wenbo/things-cloud-mcp` and MCP port `28063`. Cross-compile with `GOOS=linux GOARCH=amd64`, copy a staged binary, preserve a consistent OAuth database plus the matching credential key, and restart with `systemctl --user restart things-mcp`. Never replace the production binary before the rollback artifacts are verified.

OAuth 2.1 uses PKCE. Persistent state is in `DATA_DIR/oauth.db`; endpoints include `/authorize`, `/token`, `/register`, and `/.well-known/oauth-*`.

Feature worktrees belong under `.Codex/worktrees/`.

---

# Fork orientation

This repository is a deployment-hardened fork of [wbopan/things-cloud-mcp](https://github.com/wbopan/things-cloud-mcp). Everything above is upstream's guidance and is kept verbatim so pulls stay low-friction; this section covers what the fork changes. Where the two disagree about deployment, this section wins — the deploy branch runs as a container, not as the upstream maintainer's user service.

## What the fork adds

The delta is deployment machinery, not features. Upstream documents Fly.io hosting; the fork adds what self-hosting on a container host needs:

- a hardened `Dockerfile` (digest-pinned base images, static CGO-free binary, non-root user),
- a portable `compose.yaml` (read-only rootfs, all capabilities dropped, loopback bind by default, host specifics injected via environment),
- a GitHub Actions workflow (`.github/workflows/build-image.yml`) that builds the image on pushes to the deploy branch and pushes it to `ghcr.io/nweii/things-cloud-mcp`, so deployments pull a prebuilt image instead of compiling on the host,
- a favicon served from the server's own origin rather than a fixed hostname, since clients reject cross-origin icon sources,
- any bug fixes not yet merged upstream.

Bug fixes discovered here get contributed back upstream rather than accumulating as fork-only behavior.

## Branch layout

- `main` mirrors upstream `main` and carries no fork-only commits. Keep it that way; fast-forward it from upstream, never commit to it.
- `deploy/vX.Y.Z-hardened` is the deployed branch, pinned to the upstream version in its name, carrying the whole fork delta.
- Short-lived `fix/*` branches off `upstream/main` exist only to carry cherry-picked commits for upstream PRs.

When upstream cuts a release worth adopting, create a fresh `deploy/vX.Y.Z-hardened` branch from the new tag, re-apply the fork commits, drop any that upstream has since merged, and retire the old branch once the deployment has moved over.

## Upstreaming flow

1. Land and verify the fix on the deploy branch (it runs in a real deployment there).
2. Cherry-pick just the fix commit onto a clean `fix/*` branch off `upstream/main`.
3. Open the PR against `wbopan/things-cloud-mcp` `main`.
4. Once merged, the commit falls out of the fork's delta at the next deploy-branch cut.

Prior example: the update-path sort-index heal (upstream #18, still open). The create-path clamp that ships with the fork came the other direction — it is another contributor's commit (upstream #17), carried here before it merged.

## Working conventions

- Keep the README and this file upstream-shaped apart from the fork sections, so pulls from upstream stay low-friction.
- Never set `THINGS_DEBUG` in a deployment: it logs the plaintext Things password.
- Do not rename environment variables; existing deployments depend on the current names.
- Set `CREDENTIALS_SECRET` from the deployment's own secret store rather than letting the server generate `DATA_DIR/credentials.key`, so a data-volume backup alone cannot decrypt stored passwords.
- Image rebuilds happen only on deliberate pushes to the pinned deploy branch (or manual workflow dispatch), never implicitly.
- Keep this repo free of any deployer's host specifics (paths, addresses, UIDs, service names); `compose.yaml` takes them from the environment for that reason.

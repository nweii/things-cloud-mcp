# AGENTS.md

Orientation for this fork. For build commands, architecture, and SDK details, see CLAUDE.md — this file only covers what makes the fork different from upstream.

## What this fork is

A deployment-hardened fork of [wbopan/things-cloud-mcp](https://github.com/wbopan/things-cloud-mcp). The delta is deployment machinery, not features: upstream documents Fly.io hosting, and this fork adds what self-hosting on a container host needs. Bug fixes discovered here get contributed back upstream rather than accumulating as fork-only behavior.

## Branch layout

- `main` mirrors upstream `main` and carries no fork-only commits. Keep it that way; fast-forward it from upstream, never commit to it.
- `deploy/v1.3.2-hardened` is the deployed branch, pinned to the upstream version in its name. It carries the fork's whole delta:
  - a hardened `Dockerfile` (digest-pinned base images, static CGO-free binary, non-root user),
  - a portable `compose.yaml` (read-only rootfs, all capabilities dropped, host specifics injected via environment),
  - a GitHub Actions workflow (`.github/workflows/build-image.yml`) that builds the image on pushes to this branch and pushes it to `ghcr.io/nweii/things-cloud-mcp`, so deployments pull a prebuilt image instead of compiling on the host,
  - an MIT `LICENSE` file, a favicon served from the server's own origin, and any bug fixes not yet merged upstream.
- Short-lived `fix/*` branches off `upstream/main` exist only to carry cherry-picked commits for upstream PRs.

When upstream cuts a release worth adopting, create a fresh `deploy/vX.Y.Z-hardened` branch from the new tag, re-apply the hardening commits, and retire the old branch once the deployment has moved over.

## Upstreaming flow

1. Land and verify the fix on the deploy branch (it runs in a real deployment there).
2. Cherry-pick just the fix commit onto a clean `fix/*` branch off `upstream/main`.
3. Open the PR against `wbopan/things-cloud-mcp` `main`.
4. Once merged, the commit falls out of the fork's delta at the next deploy-branch rebase.

Prior examples: the create-path sort-index clamp (upstream #16) and the update-path sort-index heal (upstream #18).

## Working conventions

- Keep the README upstream-shaped apart from the fork banner at the top, so pulls from upstream stay low-friction.
- Never set `THINGS_DEBUG` in a deployment: it logs the plaintext Things password.
- Do not rename environment variables; existing deployments depend on the current names.
- Image rebuilds happen only on deliberate pushes to the pinned deploy branch (or manual workflow dispatch), never implicitly.
- Keep this repo free of any deployer's host specifics (paths, addresses, UIDs, service names); `compose.yaml` takes them from the environment for that reason.

# Feature: Kit-image distribution via GitHub Container Registry

## Summary

The kit build pipeline is the slowest cliff in the agentbox UX. A fresh-machine
`agentbox run` for the default kit list (`polyglot+containers+claude`) takes
30–60 minutes — apt installs, then npm + python toolchain + Go + Rust + Ruby +
Java/Kotlin + clang/lld + DB clients + nested-podman + Claude Code. Every machine
pays this cost once per kit list. For a project where the author reprovisions
multiple machines, that's the friction that makes the box-vs-host tradeoff less
appealing.

This feature pre-publishes the most common kit-list compositions to
`ghcr.io/nklisch/agentbox-kits` on every release, and teaches `agentbox build`
and `agentbox run` to pull from there before falling back to a local build. A
first run on a fresh machine goes from 30–60 minutes to 3–5 minutes. GHCR is
free for public packages; the agentbox repo is public; there's no recurring cost.

## Why now

Phase 3 of the roadmap deferred "kit image registry distribution (so first run
is `pull` not `build`)" explicitly. The deferred item was a placeholder for this:
once the kit pipeline and the easy-install story stabilized, pre-publishing images
became straightforward. The consumer side (CLI changes) is independent of the
producer side (CI publishing), so the two can ship as separate commits and the
CLI falls back gracefully if the registry has nothing yet.

## What shipped

See [the design](registry-images.design.md) for the full unit-by-unit
specification. Here's the user-facing surface.

### Config block

```toml
[registry]
enabled      = true
host         = "ghcr.io/nklisch/agentbox-kits"
verify       = "none"
pull_timeout = "5m"
```

All four keys have defaults. Shipping out of the box with `enabled = true` and
the canonical GHCR host means a new install just works without touching config.
Opt out with:

```sh
agentbox config set registry.enabled false
```

`verify = "cosign"` is reserved at the config layer (rejected at parse time with
exit 2) but not implemented yet. Set it and you'll get a clear error — it won't
silently do nothing.

### Pull path: when it fires

On every `agentbox build` (and the build step inside `agentbox run`), after a
cache miss, the builder checks three conditions before attempting a pull:

1. `[registry] enabled = true`
2. `--no-pull` was not passed
3. Every kit in the resolved list is a built-in (no user kits, no user-shadowed
   built-ins)

All three must be true. If any kit in the list is user-authored or shadows a
built-in by name, the pull path is silently skipped and the local build runs as
before. The check is over the resolved list — a user kit pulled in transitively
via `depends_on` also disables the pull path.

### What the user sees on first run

```
[registry] pulling ghcr.io/nklisch/agentbox-kits:0.3.0-a2b4c6d8e9f1
Pulling from host.containers.internal/ghcr.io/nklisch/agentbox-kits
...
[registry] pulled ghcr.io/nklisch/agentbox-kits:0.3.0-a2b4c6d8e9f1 as agentbox/a2b4c6d8e9f1
```

Subsequent runs hit the cache and skip the network entirely — same behavior as
a locally-built image. The local image tag (`agentbox/<sha[:12]>`) is unchanged.
Pulled images are retagged immediately so the lifecycle, labels, and
`agentbox.kit_image` are byte-identical to the local-build path.

### Failure handling

Pull failures are loud but non-fatal. Every failure logs to stderr as a
`[registry] ...` line and falls through to local build:

```
[registry] no image at ghcr.io/nklisch/agentbox-kits:0.3.0-a2b4c6d8e9f1; building locally
```

On auth failure:

```
[registry] auth required for ghcr.io/nklisch/agentbox-kits:0.3.0-... — try `podman login ghcr.io`; falling back to local build
```

On network error:

```
[registry] network error pulling ghcr.io/nklisch/agentbox-kits:0.3.0-...; falling back to local build
```

Pull never aborts. The worst case is the same behavior as before this feature
shipped.

### Escape hatch: `--no-pull`

For kit iteration, or when you want to confirm you can still build from source:

```sh
agentbox build polyglot,claude --no-pull
agentbox run --no-pull
```

`--no-pull` hard-skips the registry path even when the kit list is otherwise
eligible. No `[registry] pulling` lines will appear.

### Effect of user kits on pull eligibility

If you're developing a custom kit or shadowing a built-in by name, the pull
path automatically disables itself:

```sh
# Create a user-shadowed claude kit
mkdir -p ~/.config/agentbox/kits/claude
# ... edit it ...

# Now agentbox build polyglot,claude uses the local-build path.
# No pull is attempted; no [registry] output appears.
agentbox build polyglot,claude
```

This is a safety guarantee: the remote image's content can't match a
user-modified kit, so the pull path skips rather than serving a stale image.

### Doctor check

`agentbox doctor` gains a new `registry-reachable` check. It does a HEAD probe
against the registry's manifests endpoint for the well-known
`latest-polyglot-containers-claude` alias and reports OK, WARN-with-message, or
(if `[registry] enabled = false`) OK-with-skip:

```
registry-reachable  OK    ghcr.io/nklisch/agentbox-kits reachable; published images present
```

This check is warn-only. If the registry is unreachable, the build path falls
back to local build and you keep working.

Known limitation: GHCR's OCI manifest endpoint requires a bearer-token
negotiation that `podman pull` handles transparently but a plain `http.Client`
HEAD does not. Against ghcr.io specifically, the plain HEAD returns 401 even
when anonymous pull works fine. The actual pull path is unaffected — only the
doctor check shows the spurious 401. Two fixes are possible later: implement
the token dance, or change the probe target. For now, treat `registry-reachable`
as "did we get a 200" and interpret a 401 from ghcr.io as "auth handshake
required, not a real failure."

## Pre-published images

Four kit-list compositions are published per release:

| Nickname                     | Kit list                                    |
| ---------------------------- | ------------------------------------------- |
| `polyglot-containers-claude` | `base, polyglot, containers, node, claude`  |
| `polyglot-containers-codex`  | `base, polyglot, containers, node, codex`   |
| `polyglot-claude`            | `base, polyglot, node, claude`              |
| `node-claude`                | `base, node, claude`                        |

These four cover the vast majority of real usage. The list is the canonical
source at `.github/published-kits.yml`; adding a new composition is a one-PR
change there.

Three tags are published per image:

- `<version>-<sha[:12]>` — immutable, version-pinned; what the CLI looks for
- `<version>-<nickname>` — immutable, human-readable
- `latest-<nickname>` — rolling, reassigned each release

The CLI always pulls the sha-pinned tag, derived from the same hash function
that produces the local tag. The alias variants are for manual `podman pull`
when you want to inspect an image by name.

### Multi-arch

All four images are multi-arch manifests (`linux/amd64` + `linux/arm64`). The
arm64 build runs under QEMU emulation on GitHub's amd64 runners — slow (minutes
to tens of minutes for the chunky `polyglot+containers+claude` list), but that's
a release-time cost, not a user cost. The GHA cache backend reduces this for
incremental releases.

## How the CI workflow publishes

`.github/workflows/kit-images.yml` triggers on `v*` tag push (alongside the
existing `release.yml`) and on `workflow_dispatch` for re-publishing without a
new tag. For each kit list in the matrix, it:

1. Builds the agentbox CLI with the release ldflags so the version string matches.
2. Runs `agentbox build --emit-context <dir> <kits>` to stage the Dockerfile
   and kit dirs to disk without invoking podman.
3. Hands the staged context to `docker buildx build --push` with QEMU for
   multi-arch.

The `--emit-context` flag is the seam between the CLI and CI. It's also exposed
to the user for inspection:

```sh
agentbox build --emit-context /tmp/ctx polyglot,claude
ls /tmp/ctx
# Dockerfile  kits/
```

`--print-tag` (noted in the design as a follow-up one-liner) would let the
workflow derive the sha-pinned tag cleanly instead of grep-extracting it from
`--print` output. Not included in this design; the current workflow uses a
workaround.

## Decoupling: Group A and Group B

The design split the work along a clean seam:

**Group A (consumer side, commit `a2a4413`)** — `[registry]` config, the
`RemoteImageRef` helpers, `Runner.Pull`/`Tag`, the pull-then-fall-back path in
`Builder.Build`, `AllBuiltin` eligibility check, cache `source` field, `--no-pull`
flag, doctor check.

**Group B (producer side + CI, commit `14990d5`)** — `--emit-context` and
`--print-tag` flags, `.github/published-kits.yml` catalog,
`.github/workflows/kit-images.yml` workflow.

Group A ships even without Group B: everything 404s and falls back to local
build — functionally identical to before the feature shipped. Group B ships
independently because publishing images is a CI concern, not a CLI concern. This
made it safe to land Group A first and validate the pull path works before
committing to the CI workflow shape.

## Out of scope

- **Cosign signing and verification.** `[registry] verify = "cosign"` is
  reserved at the config layer and rejected at parse time, but not implemented.
  Add it when there's a credible threat model that justifies the operational
  complexity.
- **Private registry support.** The CLI doesn't `podman login` for you. If
  `host` points at a private registry, log in yourself first — auth failures
  surface clearly with a corrective hint.
- **A dedicated `agentbox pull` subcommand.** The pull-then-fall-back path
  inside `build` covers the real use cases. A standalone `pull` command adds
  surface for marginal value.
- **`--print-tag` flag on `agentbox build`.** The CI workflow currently
  grep-extracts the sha from `agentbox build --print` output. A clean
  `--print-tag` flag is a one-liner follow-up.
- **The doctor check's GHCR auth quirk.** The 401 against ghcr.io from a plain
  HTTP HEAD (described above) is a known limitation, not a bug. The actual
  pull path is unaffected.

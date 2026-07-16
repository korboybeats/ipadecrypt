# Upstream Integration Design

## Objective

Merge `londek/ipadecrypt` `main` through commit `755aba1` into the
`korboybeats/ipadecrypt` fork without rewriting the fork's public history or
dropping fork-specific behavior.

The resulting integration must retain the fork's jailbreak app, RootHide
support, App Store version selection, auth refresh, output retention policies,
self-update flow, and device/desktop workflows while incorporating all five
upstream commits currently absent from the fork.

## Considered Approaches

### Merge upstream with a dedicated merge commit (selected)

Create an integration branch from `main`, merge `upstream/main` with `--no-ff`,
resolve overlapping behavior deliberately, and validate the complete result.
This preserves the fork's existing merge-based history and release tags and
provides a clear boundary for future upstream synchronization.

### Rebase the fork onto upstream

Replaying the fork's 44 unique commits could produce a linear history, but it
would rewrite already-published commits and tags and require resolving old
integration decisions again. This is unsuitable for the existing public fork.

### Cherry-pick the five upstream commits

Cherry-picking could isolate each change, but it would duplicate upstream
commit identities and make later upstream merges harder. It offers no material
conflict advantage because the same files and behaviors overlap.

## Integration Policy

Use a normal merge commit in an isolated worktree. Do not use blanket `ours` or
`theirs` resolution. Resolve each conflict by preserving the fork's expanded
workflow and integrating the upstream behavior at the corresponding extension
point.

The integration includes:

- verbose App Store rate-limit errors;
- the `--storefront` CLI option for relevant App Store-backed commands;
- dynamic App Store authentication endpoint discovery with the upstream
  fallback behavior;
- the README note about the `SC_Info` limitation; and
- retention of a decrypted IPA when later decryption processing fails.

Existing fork flags, command aliases, interactive flows, App Store helpers,
authentication refresh, and output keep policies remain supported.

## Conflict Resolution Boundaries

The dry-run merge reports conflicts in:

- `cmd/ipadecrypt/decrypt.go`
- `cmd/ipadecrypt/main.go`
- `cmd/ipadecrypt/store.go`
- `cmd/ipadecrypt/versions.go`
- `internal/appstore/http.go`

`decrypt.go` must combine upstream storefront propagation and failure-retention
semantics with the fork's installed/App Store selection and keep-policy flow.
Successful cleanup behavior must remain unchanged; only failure paths retain
the recoverable decrypted artifact.

`main.go`, `store.go`, and `versions.go` must expose storefront selection
without removing the fork's short aliases or version-selection behavior. A
user-supplied storefront must be normalized and propagated to all applicable
store requests using the upstream mapping rules.

`internal/appstore/http.go` must retain the fork's plist normalization and
clear rate-limit diagnostics while adding dynamic authentication endpoint
discovery. Endpoint discovery must preserve upstream's fallback when discovery
cannot provide a usable URL.

## Testing and Validation

Before merging, run the existing Go test suite on the integration branch to
establish the baseline. During resolution, add focused tests where the desired
combined behavior is not already protected, following a red-green cycle. At a
minimum, verify storefront normalization/propagation, rate-limit diagnostics,
authentication endpoint fallback/discovery, and failed-decryption retention.

Final verification consists of:

```sh
gofmt -w <changed-go-files>
go test ./...
go vet ./...
go build -trimpath -ldflags="-s -w" -o /tmp/ipadecrypt-merge-check ./cmd/ipadecrypt
git diff --check main...HEAD
git status --short
```

The integration is ready for `main` only when the test, vet, build, and diff
checks succeed and the merge commit contains both parent histories. Device-side
installation or release publication is outside this integration step and
should occur only after the local integration is reviewed.

## Safety and History

Keep `main` and tag `v0.7.0-korboy.1` unchanged during integration. Work on a
new branch in an isolated worktree. Do not force-push or move existing tags.
If validation fails, leave the integration branch available for diagnosis
without modifying `main`.

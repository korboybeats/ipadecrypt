# Upstream Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge `londek/ipadecrypt` through `755aba1` into the fork while preserving all fork-specific behavior and public history.

**Architecture:** Work on `integrate-upstream-2026-07` in an isolated Git worktree. Add focused tests before the merge, then create a true two-parent merge commit and resolve each overlap semantically: the fork remains the surrounding architecture while upstream storefront selection, authentication endpoint discovery, rate-limit diagnostics, README guidance, and completed-output retention are incorporated.

**Tech Stack:** Git, Go 1.25+, Cobra, `howett.net/plist`, standard Go tests.

## Global Constraints

- Do not rewrite `main` or move tag `v0.7.0-korboy.1`.
- Preserve the jailbreak app, RootHide support, App Store version selection, auth refresh, keep policies, self-update, short aliases, and device/desktop workflows.
- Do not resolve any conflicted file wholesale with `ours` or `theirs`.
- Preserve redaction of sensitive Apple response fields in diagnostics.
- The final integration must be a merge commit with the pre-integration fork commit and `755aba1` as parents.

---

### Task 1: Establish the Isolated Baseline and Red Tests

**Files:**
- Create: `internal/appstore/storefronts_test.go`
- Create: `internal/appstore/auth_endpoint_test.go`
- Create: `cmd/ipadecrypt/store_test.go`
- Modify: `cmd/ipadecrypt/decrypt_install_test.go`

**Interfaces:**
- Consumes: `config.Config`, `config.Apple`, and the existing Go test harness.
- Produces: desired contracts for `appstore.ResolveStorefront(string) (string, error)`, `normalizeAuthEndpoint(...string) string`, `authEndpointFromText(string) string`, `accountWithStorefront(*config.Config, string) (*appstore.Account, error)`, and `shouldAbandonLocalOutput(bool) bool`.

- [ ] **Step 1: Prepare the isolated worktree and baseline**

Run:

```sh
git remote get-url upstream >/dev/null 2>&1 || git remote add upstream https://github.com/londek/ipadecrypt.git
git fetch upstream main
go mod download
go test ./...
```

Expected: the pre-merge fork test suite exits 0.

- [ ] **Step 2: Write storefront resolution tests**

Create `internal/appstore/storefronts_test.go` with table tests proving `US` and `pl` map to their canonical IDs, a numeric ID is retained, surrounding whitespace is accepted, and an unknown country returns an error.

```go
package appstore

import "testing"

func TestResolveStorefront(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "uppercase country", input: "US", want: "143441"},
		{name: "lowercase country", input: "pl", want: "143478"},
		{name: "numeric ID", input: "143441", want: "143441"},
		{name: "trimmed country", input: "  US  ", want: "143441"},
		{name: "unknown", input: "ZZ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveStorefront(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveStorefront(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ResolveStorefront(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Write account override tests**

Create `cmd/ipadecrypt/store_test.go` proving the override uses a copied account and leaves saved configuration untouched.

```go
package main

import (
	"testing"

	"github.com/londek/ipadecrypt/internal/config"
)

func TestAccountWithStorefrontOverridesCopy(t *testing.T) {
	cfg := &config.Config{Apple: config.Apple{Email: "test@example.com", StoreFront: "143478"}}
	acc, err := accountWithStorefront(cfg, "US")
	if err != nil {
		t.Fatalf("accountWithStorefront: %v", err)
	}
	if acc.StoreFront != "143441" {
		t.Fatalf("StoreFront = %q, want 143441", acc.StoreFront)
	}
	if cfg.Apple.StoreFront != "143478" {
		t.Fatalf("saved storefront mutated to %q", cfg.Apple.StoreFront)
	}
}
```

- [ ] **Step 4: Write endpoint and completed-output tests**

Create `internal/appstore/auth_endpoint_test.go` to verify fallback, native `/fast/` normalization, escaped response URL extraction, and rejection of a non-Apple host:

```go
package appstore

import "testing"

func TestNormalizeAuthEndpoint(t *testing.T) {
	const fallback = "https://auth.itunes.apple.com/auth/v1/native/fast/"
	if got := normalizeAuthEndpoint(); got != fallback {
		t.Fatalf("normalizeAuthEndpoint() = %q, want %q", got, fallback)
	}
	if got := normalizeAuthEndpoint("https://auth.itunes.apple.com/auth/v1/native"); got != fallback {
		t.Fatalf("normalized endpoint = %q, want %q", got, fallback)
	}
}

func TestAuthEndpointFromText(t *testing.T) {
	text := `redirect=https:\/\/auth.itunes.apple.com\/auth\/v1\/native`
	want := "https://auth.itunes.apple.com/auth/v1/native/fast/"
	if got := authEndpointFromText(text); got != want {
		t.Fatalf("authEndpointFromText() = %q, want %q", got, want)
	}
}

func TestNormalizeNativeAuthEndpointRejectsOtherHosts(t *testing.T) {
	if got := normalizeNativeAuthEndpoint("https://example.com/auth/v1/native"); got != "" {
		t.Fatalf("normalizeNativeAuthEndpoint() = %q, want empty", got)
	}
}
```

Add this test to `cmd/ipadecrypt/decrypt_install_test.go`:

```go
func TestShouldAbandonLocalOutput(t *testing.T) {
	if !shouldAbandonLocalOutput(false) {
		t.Fatal("incomplete output must be abandoned")
	}
	if shouldAbandonLocalOutput(true) {
		t.Fatal("completed output must be retained when later verification fails")
	}
}
```

- [ ] **Step 5: Run the focused tests and verify RED**

Run:

```sh
go test ./internal/appstore ./cmd/ipadecrypt
```

Expected: compilation fails only because the five planned functions do not exist yet.

- [ ] **Step 6: Commit the red tests**

```sh
git add internal/appstore/storefronts_test.go internal/appstore/auth_endpoint_test.go cmd/ipadecrypt/store_test.go cmd/ipadecrypt/decrypt_install_test.go
git commit -m "test: define upstream integration behavior"
```

---

### Task 2: Merge Upstream and Resolve CLI/Storefront Behavior

**Files:**
- Modify: `cmd/ipadecrypt/main.go`
- Modify: `cmd/ipadecrypt/store.go`
- Modify: `cmd/ipadecrypt/decrypt.go`
- Modify: `cmd/ipadecrypt/versions.go`
- Modify: `internal/appstore/storefronts.go`

**Interfaces:**
- Consumes: the red storefront/account/output tests from Task 1 and existing fork command paths.
- Produces: `--storefront` on `decrypt` and `versions`, a non-mutating account override, and completed local-output retention before verification.

- [ ] **Step 1: Start the merge**

```sh
git merge --no-ff --no-commit upstream/main
```

Expected: Git reports conflicts in the five files documented by the design.

- [ ] **Step 2: Resolve CLI flag declarations and registration**

In `cmd/ipadecrypt/main.go`, keep every fork variable and command alias, add `decryptStorefront` and `versionsStorefront`, and register:

```go
decrypt.Flags().StringVar(&decryptStorefront, "storefront", "", "override App Store storefront (numeric ID or two-letter country code, e.g. 143441 or US)")
versions.Flags().StringVar(&versionsStorefront, "storefront", "", "override App Store storefront (numeric ID or two-letter country code, e.g. 143441 or US)")
```

- [ ] **Step 3: Resolve account propagation**

In `cmd/ipadecrypt/store.go`, retain `withAuth` and add `accountWithStorefront`. In `decrypt.go`, build the overridden account once before App Store lookup and pass it through the relevant lookup path. In `versions.go`, use the overridden account for target lookup while retaining the fork's TUI, cache, updater, and auth-refresh flow.

- [ ] **Step 4: Resolve completed-output retention**

In `cmd/ipadecrypt/decrypt.go`, add:

```go
func shouldAbandonLocalOutput(completed bool) bool { return !completed }
```

Track `localOutputComplete := false`, use the helper in cleanup, and set it to true immediately after the output stream is synced successfully and before optional verification. Keep incomplete-stream cleanup unchanged.

- [ ] **Step 5: Harden storefront normalization**

Retain upstream's `ResolveStorefront`, but trim surrounding whitespace before numeric or country-code resolution.

- [ ] **Step 6: Remove all conflict markers and run focused tests**

```sh
rg -n '^(<<<<<<<|=======|>>>>>>>)' cmd/ipadecrypt internal/appstore
gofmt -w cmd/ipadecrypt/main.go cmd/ipadecrypt/store.go cmd/ipadecrypt/decrypt.go cmd/ipadecrypt/versions.go internal/appstore/storefronts.go
go test ./cmd/ipadecrypt ./internal/appstore
```

Expected: no conflict-marker matches; tests may still fail only in endpoint-discovery tests until Task 3 is resolved.

---

### Task 3: Resolve Dynamic Authentication and HTTP Diagnostics

**Files:**
- Add from upstream and refine: `internal/appstore/auth_endpoint.go`
- Modify: `internal/appstore/bag.go`
- Modify: `internal/appstore/http.go`
- Modify: `internal/appstore/login.go`
- Modify: `internal/appstore/types.go`
- Modify: `internal/appstore/http_test.go`

**Interfaces:**
- Consumes: fork plist normalization, response redaction, rate-limit test, and Task 1 endpoint tests.
- Produces: typed `ResponseDecodeError`, safe response previews, URL extraction, normalized native auth endpoints, and retry-on-discovered-endpoint behavior.

- [ ] **Step 1: Combine the HTTP implementations**

Keep the fork's `normalizePlist`, `responsePreview`, and sensitive-field redaction. Return a `ResponseDecodeError` for plist decode failures, using `responsePreview(data)` for its stored/displayed body and URL extraction from raw response data. Keep the clear `apple auth is rate-limited (HTTP 429)` message and return the HTTP response alongside the error.

- [ ] **Step 2: Add typed-error regression coverage**

Extend `internal/appstore/http_test.go` with this test, adding the standard `errors` import:

```go
func TestSendPreservesDiscoveryURLAndRedactsDecodeError(t *testing.T) {
	const secret = "supersecret"
	client := &Client{
		jar: &cookiejar.Jar{},
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := "passwordToken=" + secret + " https://auth.itunes.apple.com/auth/v1/native"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
	}

	var out loginResult
	_, err := client.send(http.MethodPost, "https://example.test/auth", nil, nil, formatXML, &out)
	var decodeErr *ResponseDecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *ResponseDecodeError", err)
	}
	if strings.Contains(decodeErr.Body, secret) {
		t.Fatalf("decode body leaked secret: %q", decodeErr.Body)
	}
	want := "https://auth.itunes.apple.com/auth/v1/native/fast/"
	if got := authEndpointFromResponseError(err); got != want {
		t.Fatalf("discovered endpoint = %q, want %q", got, want)
	}
}
```

- [ ] **Step 3: Resolve bag and login behavior**

Keep both nested and top-level bag endpoint fields, normalize the first valid one with fallback to `https://auth.itunes.apple.com/auth/v1/native/fast/`, and update `Login` to retry once on a newly discovered Apple native endpoint while retaining existing redirect and 2FA handling.

- [ ] **Step 4: Run endpoint tests GREEN**

```sh
gofmt -w internal/appstore/auth_endpoint.go internal/appstore/auth_endpoint_test.go internal/appstore/bag.go internal/appstore/http.go internal/appstore/http_test.go internal/appstore/login.go internal/appstore/types.go
go test ./internal/appstore ./cmd/ipadecrypt
```

Expected: all focused tests pass.

- [ ] **Step 5: Finish the merge commit**

```sh
git add README.md cmd/ipadecrypt internal/appstore
git diff --cached --check
git commit -m "merge: integrate upstream main"
```

Expected: a merge commit is created with two parents.

---

### Task 4: Full Verification and History Audit

**Files:**
- Verify: all merged files and Git history.

**Interfaces:**
- Consumes: completed merge commit from Task 3.
- Produces: evidence that the combined fork builds, tests, vets, formats, and contains both histories.

- [ ] **Step 1: Run formatting and whitespace checks**

```sh
test -z "$(gofmt -l $(git diff --name-only main...HEAD -- '*.go'))"
git diff --check main...HEAD
```

Expected: both commands exit 0 with no output.

- [ ] **Step 2: Run complete Go verification**

```sh
go test ./...
go vet ./...
go build -trimpath -ldflags="-s -w" -o /tmp/ipadecrypt-merge-check ./cmd/ipadecrypt
```

Expected: every command exits 0.

- [ ] **Step 3: Audit merge ancestry and scope**

```sh
test "$(git rev-list --parents -n 1 HEAD | wc -w | tr -d ' ')" = 3
git merge-base --is-ancestor upstream/main HEAD
git merge-base --is-ancestor main HEAD
git rev-list --left-right --count HEAD...upstream/main
git status --short
```

Expected: HEAD has two parents; both histories are ancestors; divergence is `46 0` or greater on the fork side and zero behind; status is clean.

- [ ] **Step 4: Review the final diff**

Inspect `git diff main...HEAD`, confirm the five upstream behaviors and all Global Constraints are represented, and report the isolated worktree path and merge commit without pushing or modifying `main`.

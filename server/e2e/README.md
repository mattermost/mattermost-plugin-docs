# Docs plugin — RBAC end-to-end suite

This is the **official** end-to-end suite for the docs plugin's space-permission RBAC model
(MM-69269). It is Go + [Testcontainers](https://golang.testcontainers.org/), and boots a real
Mattermost server with the plugin installed — no mocks.

It covers the seven canonical Confluence permission scenarios plus their named parity gaps, and is
the authoritative behavioral spec for them.

## Why a locally built core image

The core changes this epic depends on (the `Space` channel type, the atomic per-page permission
roles, the seeded preset schemes) live on an **unmerged** core branch, so no published Mattermost
image carries them yet. `build/build-core-image.sh` cross-compiles `./cmd/mattermost` from that
branch and packages it into a minimal Docker image (`$CORE_IMAGE`, default `mm-docs-rbac-core:dev`).

Building locally is only needed for a branch that has not been pushed. Core's own CI publishes a
server image for every commit as `mattermostdevelopment/mattermost-team-edition:<7-char-sha>`, so
once the paired core change is up as a PR, `CORE_IMAGE` can point at that tag and no local image
build is required — that is exactly how the `e2e` job in `.github/workflows/ci.yml` runs, reading
the tag from the `CORE_IMAGE` repository variable. That stays the shape after the core changes
merge and ship: `CORE_IMAGE` always names a per-commit sha image, because the pin check compares
it against `build/core-commit.txt`, which only ever holds a commit sha.

Which core commit that variable is allowed to name is recorded in `build/core-commit.txt`, and CI
fails the suite when the two disagree. The pin lives in a file of its own rather than being read
off the `go.mod` `server/public` version, because most of the RBAC implementation sits outside the
public module: a core commit can change the scheme guards and role composition these scenarios
assert on without moving that version at all. Bump the pin in the same PR that depends on the newer
core behaviour.

The image this script builds is **API-only**: server binary plus i18n, templates and fonts, and an
empty `client/`. That is all the suites here need — every assertion is an HTTP call.

The browser suite under `e2e-tests/playwright/` boots its **own** Testcontainers server from the
same pinned core image this script builds (it reads `MM_IMAGE`; CI passes the same `CORE_IMAGE` both
suites consume, so the two can never assert against different servers). It therefore needs an image
that serves the webapp, which the API-only image above does not — see
`e2e-tests/playwright/README-VENDORED.md` for how that suite is run.

A bare tag (no `/`) is treated as locally built: if it is absent the suite fails immediately and
names the build script, rather than letting Testcontainers fail mid-boot. A namespaced tag is
assumed pullable and is left to Testcontainers to fetch.

## Running it

Requires Docker.

```sh
make test-e2e-server
```

This ensures the plugin bundle exists (`make dist` if it is missing or carries no linux binary for
the Docker daemon's architecture), builds the core image with `build/build-core-image.sh` unless
`CORE_IMAGE` is namespaced, then runs:

```sh
go test -tags e2e -count=1 -v ./server/e2e/...
```

The container boots once for the whole suite (container startup is slow) and is torn down after
all tests finish. Every scenario creates its own space and it is deleted at the end of the run.

## Enterprise license

The suite boots the server with an Enterprise license, and fails at startup without one. Three
subtests need it: the scheme-backed permission sets (scenario 8 and the `delete_page` default)
drive core's `CreateScheme`, gated on the `CustomPermissionsSchemes` feature, and scenario 6's
read-only guest reviewer drives the real `POST /users/{id}/demote`. Unlicensed, all three answer
with a 501 that no assertion can read as a pass or a failure of the behavior under test.

The license is never committed. Supply it either way:

| Variable | Holds | Used by |
| --- | --- | --- |
| `MM_LICENSE` | the license itself | CI, from the organization secret |
| `MM_LICENSE_FILE` | a path to a file holding it | a local run |

```sh
MM_LICENSE_FILE=/path/to/mattermost.mattermost-license make test-e2e-server
```

CI reads `MM_E2E_TEST_LICENSE_ONPREM_ENT`, the organization secret the server and the other plugin
repos already use. It is granted to selected repositories, so a new repository needs an org admin
to add it before the e2e job can pass.

Absence is a hard error naming both variables rather than a skip, in keeping with the repo's rule
that a missing prerequisite fails loudly instead of quietly narrowing what the run proves.

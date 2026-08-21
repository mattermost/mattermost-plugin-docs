# Browser E2E suite

Playwright specs driving a real Mattermost server in Docker, with the plugin bundle installed into
it. The container helper (`tests/helpers/mmcontainer.ts`) boots the server, creates the admin and
the team, and installs the bundle before the first spec runs.

## Running it

```bash
make test-e2e
```

That runs the authoring specs against `mattermostdevelopment/mattermost-enterprise-edition:master`,
unlicensed — the default the CI `e2e-playwright-tests` job takes.

### The space-permission specs

They are excluded from that run (`testIgnore` in `playwright.config.ts`) because they need a core
image carrying the paired branch's RBAC work and an Enterprise license. `MM_E2E_SPACE_PERMISSIONS`
selects both the specs and the container setup they require — see `tests/helpers/mode.ts`:

```bash
MM_E2E_SPACE_PERMISSIONS=true \
MM_IMAGE=mattermostdevelopment/mattermost-team-edition:<7-char-sha> \
MM_LICENSE_FILE=/path/to/license \
make test-e2e
```

Or against a server you are already running, built from the paired core branch — no container, no
image pin. This seeds real data into that server:

```bash
MM_E2E_SPACE_PERMISSIONS=true MM_E2E_USE_EXISTING_SERVER=true make test-e2e
```

On an arm64 machine add `DOCKER_DEFAULT_PLATFORM=linux/amd64` to the container form: core CI
publishes these images for amd64 only, so the pull otherwise fails with `no matching manifest for
linux/arm64/v8`. CI runners are amd64 and need nothing. Expect the emulated server to boot slowly —
the migration wait has room for it.

The sha is the commit in `build/core-commit.txt`. Core CI's per-commit images are **not retained
indefinitely** — if the pull 404s, bump the pin to a commit whose image is still published:

```bash
curl -s -o /dev/null -w '%{http_code}' \
  https://hub.docker.com/v2/repositories/mattermostdevelopment/mattermost-team-edition/tags/<7-char-sha>/
```

In CI this run is its own job, `e2e-playwright-space-permissions`, gated on the `CORE_IMAGE`
repository variable and the license secret — so the authoring job above keeps running on every PR
regardless. The suite is **not parallel-safe**: `setGuestAccountsEnabled` (`tests/helpers/guest.ts`)
mutates a server-wide setting. See `workers` in `playwright.config.ts`.

## What the permission run adds to the harness

Each of these applies only under `MM_E2E_SPACE_PERMISSIONS`, and should be revisited once the core
changes ship in a release.

| Where | What | Why |
|---|---|---|
| `tests/helpers/mmcontainer.ts` | `MM_IMAGE` required (`resolveImage()`), instead of the `:master` default | The space-permission work lives on the paired core branch. `:master` boots, passes the `EnableDocs` check, then fails every permission assertion for a reason nothing names. |
| `tests/helpers/mmcontainer.ts` | `resolveLicense()` + `applyLicense()` (`mmctl license upload-string`) | The pooled custom-scheme paths drive core's `CreateScheme`, gated on `CustomPermissionsSchemes`. Unlicensed, permission routes answer 501. Mirrors `server/e2e/container_test.go`'s `MM_LICENSE` / `MM_LICENSE_FILE` contract. |
| `tests/helpers/mmcontainer.ts` | `assertSupportsSpacePermissions()` probes the `docs_pg_create` role | Neither the version check nor the `EnableDocs` check can tell a core image that predates the RBAC work — the flag exists on master. Turns unexplained 403s into one named setup failure. |
| `tests/helpers/mmcontainer.ts` | `waitForPhase2Migration()` before the first spec runs | The advanced-permissions phase-2 migration runs as a post-boot job, and every scheme-backed route answers 501 until it finishes — the first `CreateSpace` fails with `app.schemes.is_phase_2_migration_completed.not_completed`. Mirrors `server/e2e/container_test.go`. |

Three more changes apply to every run, permission specs or not:

| Where | What | Why |
|---|---|---|
| `tests/helpers/mmcontainer.ts` | `MM_SERVICEENVIRONMENT: 'test'` in the container env | The published core images are production builds, which default to the production service environment and reject a test/development license outright. Without this the license step fails, not a spec. |
| `tests/helpers/mmcontainer.ts` | `exec()` takes a `displayCommand` override; `applyLicense` passes a redacted form | The failure message is built from the whole command, so a rejected license would otherwise print itself into the terminal and into the CI job log. |
| `tests/helpers/docs.ts` | `apiRoot` exported | The helpers throw on a non-OK response, which is wrong when the assertion *is* the 403. The permission specs probe the route directly. |

## Permission specs

| Spec | Covers |
|---|---|
| `tests/docs/space_permissions.spec.ts` | The plugin's own surface: space-default permission toggles, view access, and the settings entry's member gate. Includes the end-to-end chain — an admin's toggle in the UI changing another member's server-enforced authority. |
| `tests/docs/system_console_space_permissions.spec.ts` | Core's `Spaces` permission group in the System Console, and the cross-boundary assertion that revoking `create_space` there stops a member creating a Docs space. Also `tests/pages/system_scheme_permissions_page.ts`. |

The System Console group is core's code, on the paired branch. It is driven from here because the
*consequence* of changing it is only observable with the plugin installed — core alone has no route
that consults `create_space`. If that test starts failing after a core bump, suspect core first.

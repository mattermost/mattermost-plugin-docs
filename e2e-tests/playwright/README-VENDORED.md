# Vendored Playwright suite

This directory is copied from [mattermost-plugin-docs#19](https://github.com/mattermost/mattermost-plugin-docs/pull/19)
(`MM-70125-playwright-e2e-infra`) so the permissions epic is not blocked on that PR landing, and so
this repo's browser suite matches the layout the rest of the team will use.

**Vendored at upstream commit `810f08b6b5e7d73b0a46d5d328184e76de71aa52`.** Record the sha whenever
this directory is re-synced. Naming only the PR is not enough: a PR branch moves, and without a sha
nothing distinguishes "we chose to differ here" from "upstream changed and we never picked it up".
An earlier sync recorded only the PR, and the directory silently fell twelve upstream commits behind
— including two fixes (per-user seeded passwords, a `0o600` state file) that read as deliberate
local regressions until the sha was reconstructed from the commit dates.

Re-check with:

```bash
git clone --depth 1 --branch MM-70125-playwright-e2e-infra \
  https://github.com/mattermost/mattermost-plugin-docs.git /tmp/pr19
diff -rq /tmp/pr19/e2e-tests/playwright e2e-tests/playwright \
  -x node_modules -x package-lock.json -x logs -x results -x test-results
```

Every file it reports must be accounted for by a row below. Nothing else should differ.

**Treat upstream as the source of truth.** Keep local changes few, and comment each one at its site
with a note naming this file, so reconciliation is a short, reviewable list rather than an
archaeology exercise.

## Why it replaced the previous `e2e/`

The earlier suite ran in *external mode*: it drove a server the developer had already started, which
is why it could never be wired into CI. It is deleted rather than kept alongside — two browser suites
would mean two harnesses to maintain and one of them silently unrun. Its single spec also targeted
selectors that never existed (`menuitem 'Space permissions'`, `heading /^Permissions for /`) and
assumed a space was already present in the sidebar, so nothing of value was lost.

## Local deltas

| # | Where | Change | Why |
|---|---|---|---|
| 1 | `tests/helpers/mmcontainer.ts` | `MM_IMAGE` is **required** (`requireImage()`); upstream defaults to `…enterprise-edition:master` | The space-permission work lives on the paired core branch. `:master` boots, passes the `EnableDocs` check, then fails every permission assertion for a reason nothing names. |
| 2 | `tests/helpers/mmcontainer.ts` | `resolveLicense()` + `applyLicense()` (`mmctl license upload-string`) | The pooled custom-scheme paths drive core's `CreateScheme`, gated on `CustomPermissionsSchemes`. Unlicensed, permission routes answer 501. Mirrors `server/e2e/container_test.go`'s `MM_LICENSE` / `MM_LICENSE_FILE` contract. |
| 3 | `tests/helpers/mmcontainer.ts` | `assertSupportsSpacePermissions()` probes the `docs_pg_create` role | Neither the version check nor the `EnableDocs` check can tell a core image that predates the RBAC work — the flag exists on master. Turns unexplained 403s into one named setup failure. |
| 4 | `tests/helpers/mmcontainer.ts` | `MM_SERVICEENVIRONMENT: 'test'` in the container env | The published core images are production builds, which default to the production service environment and reject a test/development license outright. Without this the license step fails, not a spec. |
| 5 | `tests/helpers/mmcontainer.ts` | `waitForPhase2Migration()` before the first spec runs | The advanced-permissions phase-2 migration runs as a post-boot job, and every scheme-backed route answers 501 until it finishes — the first `CreateSpace` fails with `app.schemes.is_phase_2_migration_completed.not_completed`. Mirrors `server/e2e/container_test.go`. |
| 6 | `tests/helpers/mmcontainer.ts` | `exec()` takes a `displayCommand` override; `applyLicense` passes a redacted form | Upstream builds its failure message from the whole command, so a rejected license printed itself into the terminal and would print into the CI job log. |
| 7 | `tests/helpers/docs.ts` | `apiRoot` exported | The helpers throw on a non-OK response, which is wrong when the assertion *is* the 403. The permission specs probe the route directly. |
| 8 | `../../.github/workflows/ci.yml` | `e2e-playwright-tests` sets `MM_IMAGE` from `CORE_IMAGE`, requires the license, and carries the `e2e` job's `CORE_IMAGE`/fork gating | Same server and same preconditions as the Go suite, so the two cannot assert against different cores. |
| 9 | `../../.github/actions/verify-core-image-pin/` | Pin verification extracted from the `e2e` job's inline step and shared | Both suites now need it; duplicating the bash would let them drift on what "correctly pinned" means. |
| 10 | `../../Makefile` | Upstream's `test-e2e` is named `test-e2e-playwright`; the Go suite keeps `test-e2e-server` | Both PRs define `make test-e2e` for different suites. Renaming both makes the collision explicit instead of letting one silently win. |
| 11 | `tests/helpers/guest.ts` | New: `demoteToGuest()` and `setGuestAccountsEnabled()` | A guest is the one principal that must never hold a per-member grant, so the permission specs need one. `setGuestAccountsEnabled` mutates a **server-wide** setting, which is why this suite is not parallel-safe (see `playwright.config.ts`'s `workers`). |
| 12 | `tests/pages/space_settings_modal_page.ts` | New page object: the Space Settings dialog and its Permissions tab | The surface the permission specs drive. Upstream has no equivalent because it never opens this dialog. |
| 13 | `tests/pages/space_page.ts` | `expectOpen()` matches the space header's own button inside `main`, not text anywhere on the page | The sidebar row, the just-clicked switcher result and a toast all carry the space title, so upstream's unscoped `.first()` can resolve before the space view mounts. The permission specs navigate far more than the authoring spec, which is what made the ambiguity bite. Not a spaces requirement — worth sending upstream instead of holding here. |
| 14 | `tests/pages/share_space_modal_page.ts` | Fixed `'Share space'` dialog name and an explicit `Add` button, vs upstream's title-prefix match and commit-on-select | **Not a spaces delta — branch staleness.** `master` reworked this modal (`0436350`, "UI polish: private-by-default spaces, Share modal…") and upstream tracks that rework; this branch has not merged that commit. Merge master, then take upstream's page object verbatim and delete this row. |

Deltas 4–7 are properties of running against an unreleased core branch under a license, not of this
epic, so they are the ones most likely to survive reconciliation — 4, 5 and 7 until the core changes
ship, and 6 indefinitely, since it is a straight bug fix worth sending upstream.

Deltas 13 and 14 should not outlive reconciliation at all: 13 belongs upstream, and 14 disappears the
moment this branch merges master.

## Specs added here (not vendored)

| Spec | Covers |
|---|---|
| `tests/docs/space_permissions.spec.ts` | The plugin's own surface: space-default permission toggles, view access, and the settings entry's member gate. Includes the end-to-end chain — an admin's toggle in the UI changing another member's server-enforced authority. |
| `tests/docs/system_console_space_permissions.spec.ts` | Core's `Spaces` permission group in the System Console, and the cross-boundary assertion that revoking `create_space` there stops a member creating a Docs space. Also `tests/pages/system_scheme_permissions_page.ts`. |

The System Console group is core's code, on the paired branch. It is driven from here because the
*consequence* of changing it is only observable with the plugin installed — core alone has no route
that consults `create_space`. If that test starts failing after a core bump, suspect core first.

## Reconciling when #19 ships

1. Replace this directory with upstream's, then re-apply deltas **1–7 and 10** — every row above that
   lands inside this directory or is keyed to it. (An earlier version of this list said "1–3", which
   would have silently dropped the license, service-environment, migration-wait, redaction and
   `apiRoot` deltas.) Rows 11 and 12 are new files upstream does not have, so they survive a wholesale
   replace untouched; 13 and 14 should be dropped rather than re-applied — see their rows. Prefer
   upstream's structure wherever it has diverged.
2. Once the core changes are in a released server, deltas 1–3 should mostly **go away**: restore
   upstream's `:master` default, drop `requireImage()`, and drop the role probe. The license may
   still be needed — check whether `CustomPermissionsSchemes` still gates `CreateScheme`.
3. Settle the `make test-e2e` name with whoever owns #19 rather than keeping this repo's rename
   indefinitely.
4. Delete this file when nothing above is still true.

## Running it

Requires Docker, a core image carrying the paired branch, and a license:

```bash
MM_IMAGE=mattermostdevelopment/mattermost-team-edition:<7-char-sha> \
MM_LICENSE_FILE=/path/to/license \
make test-e2e-playwright
```

On an arm64 machine add `DOCKER_DEFAULT_PLATFORM=linux/amd64`: core CI publishes these images for
amd64 only, so the pull fails with `no matching manifest for linux/arm64/v8` without it. CI runners
are amd64 and need nothing. Expect the emulated server to boot slowly — the migration wait above has
room for it.

The sha is the commit in `build/core-commit.txt`. Core CI's per-commit images are **not retained
indefinitely** — if the pull 404s, bump the pin to a commit whose image is still published:

```bash
curl -s -o /dev/null -w '%{http_code}' \
  https://hub.docker.com/v2/repositories/mattermostdevelopment/mattermost-team-edition/tags/<7-char-sha>/
```

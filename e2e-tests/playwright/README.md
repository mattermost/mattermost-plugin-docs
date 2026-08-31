# Browser E2E suite

Playwright specs driving a real Mattermost server in Docker, with the plugin bundle installed into
it. The container helper (`tests/helpers/mmcontainer.ts`) boots the server, creates the admin and
the team, and installs the bundle before the first spec runs.

## Running it

```bash
make test-e2e
```

That runs the authoring specs unlicensed against
`mattermostdevelopment/mattermost-enterprise-edition:master`, overridable locally with `MM_IMAGE`.
In cloud CI, a PR can select an unmerged core build for both E2E jobs with a description marker:

```html
<!-- e2e-core-commit: 0123456789abcdef0123456789abcdef01234567 -->
```

The marker is read only from the current PR event. Without it — including pushes to `master` and
later PRs — CI uses the master image. Keep exactly one: CI fails both E2E jobs on a marker whose
SHA is not a full lowercase 40-character SHA, or on more than one marker. The words `e2e-core-commit:`
in ordinary prose are ignored.

### The space-permission specs

They are excluded from that run (`testIgnore` in `playwright.config.ts`) because they additionally
need an Enterprise license. `MM_E2E_SPACE_PERMISSIONS` selects only the permission specs and the
licensed container setup they require — see `tests/helpers/mode.ts`. Keeping the modes separate
also ensures the unlicensed preset-only authoring scenario is not collected again just to skip:

```bash
MM_E2E_SPACE_PERMISSIONS=true \
MM_IMAGE=mattermostdevelopment/mattermost-enterprise-edition:<paired-core-tag> \
MM_LICENSE_FILE=/path/to/license \
make test-e2e
```

`<paired-core-tag>` is the first seven characters of the paired core commit, which is what the PR
marker resolves to in CI. `MM_LICENSE_FILE` points at any Enterprise license file; CI reads the
`MM_E2E_TEST_LICENSE_ONPREM_ENT` secret into `MM_LICENSE` instead. Until the paired core work is
on `master`, the marker-less CI run of this job has no image that carries the space roles.

Or against a server you are already running, built from the paired core branch — no container, no
image pin. This seeds real data into that server and, before the specs run, resets the server-wide
`team_user` role to the suite's baseline (`read_space`, `create_space`; no `manage_space`,
`delete_space`):

```bash
MM_E2E_SPACE_PERMISSIONS=true MM_E2E_USE_EXISTING_SERVER=true make test-e2e
```

On an arm64 machine the container form runs the server emulated: core CI publishes these images for
amd64 only, so a native pull fails with `no matching manifest for linux/arm64/v8`. The container
helper detects that and requests `linux/amd64` itself, so nothing needs setting — override with
`MM_E2E_PLATFORM` (or `DOCKER_DEFAULT_PLATFORM`, which it honors) once an arm64 image exists. CI
runners are amd64 and take the native path. Expect the emulated server to boot slowly — the
migration wait has room for it.

In CI this run is its own job, `e2e-playwright-space-permissions`. It resolves the same core selector
as the current authoring job; unlike authoring, it additionally requires the license secret. The
suite is **not parallel-safe**: `setGuestAccountsEnabled`
(`tests/helpers/guest.ts`) and the System Console cases mutate server-wide state. Permission mode
therefore uses one worker locally as well as in CI; see `workers` in `playwright.config.ts`.

## What the harness carries for the paired core branch

All of it should be revisited once the core changes ship in a release.

Permission mode and its CI job add:

| Where | What | Why |
|---|---|---|
| `tests/helpers/mmcontainer.ts` | `resolveLicense()` + `applyLicense()` (`mmctl license upload-string`) | The guest scenarios demote a user to a guest, which needs `GuestAccountsSettings.Enable` — a licensed feature. Accepts the license as `MM_LICENSE` or `MM_LICENSE_FILE`. |
| `tests/helpers/mmcontainer.ts` | `assertSupportsSpacePermissions()` probes the `docs_pg_create` role | Neither the version check nor the `EnableDocs` check can tell a core image that predates the RBAC work — the flag exists on master. Turns unexplained 403s into one named setup failure. |

These apply to every run, permission specs or not:

| Where | What | Why |
|---|---|---|
| `.github/actions/resolve-e2e-core-image/action.yaml` | A per-PR core-SHA marker resolved for both cloud jobs | Feature PRs can test their own unmerged core commit, while PRs without a marker and all master runs use the current master image. |
| `tests/helpers/mmcontainer.ts` | `waitForPhase2Migration()` before the first spec runs | The advanced-permissions phase-2 migration runs as a post-boot job, and core reads a scheme through that gate whichever route asks — until it finishes, the first `CreateSpace` fails with `app.schemes.is_phase_2_migration_completed.not_completed`. |
| `tests/helpers/mmcontainer.ts` | `MM_SERVICEENVIRONMENT: 'test'` in the container env | The published core images are production builds, which default to the production service environment and reject a test/development license outright. Without this the license step fails, not a spec. |
| `tests/helpers/mmcontainer.ts` | `exec()` takes a `displayCommand` override; `applyLicense` passes a redacted form | The failure message embeds the whole command and is printed to the terminal and the CI job log, so the license must not appear in the command it reports. |
| `tests/helpers/docs.ts` | Fixture-only Docs helpers | APIs arrange prerequisite spaces, pages, and memberships; every permission outcome is observed through the browser. |

## Permission specs

| Spec | Covers |
|---|---|
| `tests/docs/space_permissions.spec.ts` | All default and per-member toggles in both directions; public/private; page create/edit/rename/delete; roster add/remove/leave; archive; and system admin, space admin, manage-only, delete-only, member, non-member, former member, and invited/uninvited guest personas. |
| `tests/docs/system_console_space_permissions.spec.ts` | All four core `Spaces` controls, with browser-observed consequences for discovery, creation, management, and archive. Also `tests/pages/system_scheme_permissions_page.ts`. |
| `tests/docs/new_space_default_space_permissions.spec.ts` | The plugin's System Console preset selector; omitted create defaults, future-space-only changes, and explicit per-space override precedence. Also `tests/pages/docs_plugin_settings_page.ts`. |

The System Console group is core's code, on the paired branch. It is driven from here because the
*consequence* of changing it is only observable with the plugin installed — core alone has no route
that consults `create_space`. If that test starts failing after a core bump, suspect core first.

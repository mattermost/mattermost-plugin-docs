# Mattermost Docs Plugin

Docs for Mattermost — hierarchical pages, rich-text editing, comments, versioning, and Confluence import, as a self-contained plugin.

## Development

### Build

```bash
make dist
```

This builds the server binaries and the webapp bundle and packages them into a `.tar.gz` under `dist/`.

### Deploy to a local server

```bash
make deploy
```

Builds and uploads the plugin to a local Mattermost server via `pluginctl`. Configure the target server through the environment variables `pluginctl` reads (see `build/bin/pluginctl` and the `deploy` target in the `Makefile`).

### Test and lint

```bash
make test
make check-style
```

`make test` requires a reachable Postgres instance; point `TEST_DATABASE_POSTGRESQL_DSN` at it, or run against the standard local dev Postgres if unset. Tests fail (rather than skip) when the DSN is unreachable.

### End-to-end tests

```bash
make test-e2e
```

Runs the Playwright suite in `e2e-tests/playwright/`. **Requires Docker**: the suite starts
its own throwaway Mattermost and Postgres via testcontainers, installs the freshly built
plugin into it, and tears everything down afterwards. Nothing touches your local dev server.

The Docs plugin needs a server built with Docs core support — the `EnableDocs` feature flag and the
Space channel type — which stock releases do not yet ship. The suite defaults to
`mattermostdevelopment/mattermost-enterprise-edition:master`. Set `MM_IMAGE` locally when testing
plugin work that depends on an unmerged core image.

Cloud CI has the same default. A feature PR can select its own core commit for both E2E jobs by
adding this exact marker to that PR's description:

```html
<!-- e2e-core-commit: 0123456789abcdef0123456789abcdef01234567 -->
```

The SHA is scoped to that PR event. PRs without their own marker, and all runs on `master`, continue
to use the master image. Keep exactly one marker: CI fails both E2E jobs when a marker's SHA is not a
full lowercase 40-character SHA, or when the description carries more than one marker. The words
`e2e-core-commit:` in ordinary prose are ignored.

Set `MM_IMAGE` to override that default — to reproduce a run against another build, or to
bisect a server-side regression:

```bash
MM_IMAGE=mattermostdevelopment/mattermost-team-edition:<tag> make test-e2e
```

To run against a Mattermost server you are already running instead of a container:

```bash
cd e2e-tests/playwright && \
  MM_E2E_USE_EXISTING_SERVER=true MM_SERVICESETTINGS_SITEURL=http://localhost:8065 npm test
```

This seeds real teams and users into that server — and in permission mode also resets the
server-wide `team_user` role to the suite's baseline — so it is opt-in through
`MM_E2E_USE_EXISTING_SERVER` alone — exporting `MM_SERVICESETTINGS_SITEURL`, as most
Mattermost dev shells do, is not enough to trigger it.

The space-permission specs are excluded from the default run; `MM_E2E_SPACE_PERMISSIONS=true`
selects them. They need a core image carrying the paired branch's space roles and an Enterprise
license:

```bash
MM_E2E_SPACE_PERMISSIONS=true \
MM_IMAGE=mattermostdevelopment/mattermost-team-edition:<paired-core-tag> \
MM_LICENSE_FILE=/path/to/license \
make test-e2e
```

`<paired-core-tag>` is the first seven characters of the paired core commit — the same value the
PR marker above resolves to. The license is any Enterprise license file; CI supplies its own from
the `MM_E2E_TEST_LICENSE_ONPREM_ENT` secret. `e2e-tests/playwright/README.md` covers the
existing-server form and the arm64 notes.

Other useful scripts, run from `e2e-tests/playwright/`:

```bash
npm run playwright:test:headed   # watch the browser
npm run playwright:ui            # interactive UI mode
npm run test:video               # record video of every test
npm run check                    # lint
npm run check-types              # typecheck
```

#### Recording video

Video is off by default — traces (kept on failure) cover most debugging at a fraction of
the size. Opt in with `PW_VIDEO`, which takes Playwright's own mode names:

```bash
PW_VIDEO=on npm test                  # every test
PW_VIDEO=retain-on-failure npm test   # only tests that failed
```

Recordings land next to the other artifacts in `test-results/`. A single spec can opt in
on its own with `test.use({video: 'on'})`.

## Documentation


See the [Mattermost plugin development guide](https://developers.mattermost.com/integrate/plugins/) for plugin structure, server/webapp hooks, and the release process.

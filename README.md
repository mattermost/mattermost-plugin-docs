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
make test-e2e-server
make test-e2e-playwright
```

Two suites, both against a real Mattermost server built from the paired core branch and started in Docker. `test-e2e-server` runs the API-level Go suite in `server/e2e/`; `test-e2e-playwright` runs the browser-level suite in `e2e-tests/playwright/`. Both need the pinned core image and an Enterprise license — see `server/e2e/README.md` for the image and license prerequisites, and `e2e-tests/playwright/README-VENDORED.md` for what the browser suite carries on top of upstream's harness.

**Both require Docker**: each suite starts its own throwaway Mattermost and Postgres via
testcontainers, installs the freshly built plugin into it, and tears everything down
afterwards. Nothing touches your local dev server.

The space-permission work these suites assert lives on the paired core branch, not on any
published release, so there is no usable default image: name the image core CI published
for the commit in `build/core-commit.txt`.

```bash
MM_IMAGE=mattermostdevelopment/mattermost-team-edition:<7-char-sha> make test-e2e-playwright
```

The browser suite can also run against a Mattermost server you are already running instead
of a container:

```bash
cd e2e-tests/playwright && \
  MM_E2E_USE_EXISTING_SERVER=true MM_SERVICESETTINGS_SITEURL=http://localhost:8065 npm test
```

This seeds real teams and users into that server, so it is opt-in through
`MM_E2E_USE_EXISTING_SERVER` alone — exporting `MM_SERVICESETTINGS_SITEURL`, as most
Mattermost dev shells do, is not enough to trigger it.

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

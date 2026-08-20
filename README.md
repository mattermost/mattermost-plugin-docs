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

## Documentation


See the [Mattermost plugin development guide](https://developers.mattermost.com/integrate/plugins/) for plugin structure, server/webapp hooks, and the release process.

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

Runs the containerized server E2E suite in `server/e2e/` (a real Mattermost server built from the paired core branch, via Docker — see `server/e2e/README.md` for the image and license prerequisites). The browser-level Playwright suite lives separately in `e2e/` with its own setup, documented in `e2e/README.md`.

## Documentation


See the [Mattermost plugin development guide](https://developers.mattermost.com/integrate/plugins/) for plugin structure, server/webapp hooks, and the release process.

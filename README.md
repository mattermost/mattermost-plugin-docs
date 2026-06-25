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

## Documentation

See the [Mattermost plugin development guide](https://developers.mattermost.com/integrate/plugins/) for plugin structure, server/webapp hooks, and the release process.

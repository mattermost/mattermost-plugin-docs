# Docs plugin — browser end-to-end suite

Playwright specs that drive the plugin's own UI in a real browser. This is the third of three
suites, and the narrowest:

| Suite | What it proves | Server |
| --- | --- | --- |
| `webapp` Jest | the modal renders and sends the right requests | none (mocked reads) |
| `server/e2e` | every endpoint enforces the permission model | its own, via Testcontainers |
| `e2e` (this one) | the UI reaches those endpoints from a real browser | one that is already running |

## Running it

**The suite is destructive to its target server.** `pw.initSetup()` replaces the entire server
config with the library's baseline (disabling unrelated plugins) and creates users and teams;
there is no restoring teardown. Point it ONLY at a disposable dev server — never one whose
configuration you care about. (The spec restores `PluginSettings.Directory`/`ClientDirectory`
after the reset so the server stays deployable, but nothing else is restored.)

The suite never starts a server. Point it at one that is already running, serving the webapp,
with the plugin deployed and `EnableDocs` on:

```sh
npm install                                    # once
npx playwright install chromium                # once
PW_BASE_URL=http://localhost:8065 npm test
```

`PW_BASE_URL` defaults to `http://localhost:8065`, so a stock local server needs no variable at
all. Point it at whichever port your own server runs on.

Each prerequisite is asserted rather than arranged, and names its own remedy on failure.
`EnableDocs` is read once at boot, so a spec cannot switch it without restarting the server out
from under the browser; and deploying a plugin bundle would make the suite own a server it is
deliberately designed not to own.

## Why external mode, and no Testcontainers

`server/e2e` boots its own core image because it must assert against an unmerged core branch. This
suite does not need that: it asserts the plugin's UI against whatever server you already have. That
keeps a run at seconds rather than a container boot, and keeps the suite free of any Docker or
image-pin dependency.

The one cost is that the suite is not wired into CI — CI has no server serving a webapp with the
plugin deployed. Run it locally against your dev server.

## Coverage this suite does not yet have

The permission round-trip — toggle a capability, save, confirm the server stored it — is **not**
covered here, and cannot be until the webapp's data source is API-backed.

`webapp/src/data/index.ts` still exports `mockDataSource`, so the spaces in the sidebar are
fixtures. The permissions modal, by contrast, talks to the real API (`client/space_permissions.ts`).
A browser test that opens the modal from the sidebar therefore asks the server about a space id it
has never heard of, and gets a 403 — the modal is reachable, but nothing behind it is real.

Until then the round-trip is covered one layer down, in `server/e2e/scenarios_test.go`, against a
real server with nothing mocked. What is missing is only the browser's half of that hop.

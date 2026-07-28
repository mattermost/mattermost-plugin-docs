# Manually verifying Confluence bundle import (upload + inspection)

This walks through exercising the import upload/inspection API by hand. It covers what is
implemented today: a bundle can be uploaded, validated, and staged, and its findings can be read
back.

> **No page is written yet.** The preflight worker and page execution are not implemented, so an
> accepted job stops at `awaiting_source` (existing-Space target) or `queued_preflight` (new-Space
> target) and stays there. That is the expected end state for now — see
> `implementation-plans/confluence-page-import.md`, phases 4 and 5.

## Prerequisites

- The plugin is deployed and enabled, and the server has the `EnableDocs` feature flag on. Without
  it every route returns `501` and the plugin logs a warning at activation.
- A user session token, exported as `TOKEN`, plus your server URL and the plugin's API root:

```bash
export MM_URL="http://localhost:8065"
export API="$MM_URL/plugins/com.mattermost.docs/api/v1"
export TOKEN="<your personal access token or session token>"
export AUTH="Authorization: Bearer $TOKEN"
```

## 1. Generate a bundle

You do not need mmetl. The repository ships a generator that writes a bundle satisfying the same
contract the importer enforces (it is built by `server/internal/importfixture`, the very package the
automated tests use, so what you upload by hand matches what CI exercises):

```bash
# A minimal, clean bundle.
go run ./server/cmd/genimportbundle -out /tmp/bundle.zip

# A richer bundle that also produces inspection findings: a comment, an attachment record,
# a restricted page, a manifest-only restricted entry, and a producer warning.
go run ./server/cmd/genimportbundle -out /tmp/bundle.zip -pages 6 -with-findings
```

It prints the counts and checksums it wrote, so you can compare them against the API response:

```text
wrote /tmp/bundle.zip (2314 bytes)
  archive sha256 : d4f4b5fa…
  jsonl sha256   : 58758e10…
  space key/name : DOCS / Docs
  advisory team  : myteam
  pages          : 6
  comments       : 1
  attachments    : 1
  restricted     : 2
```

To exercise the rejection paths, ask it to break one rule (see step 5).

If you have a real mmetl export, use that instead — the API accepts any conforming v2 bundle:

```bash
mmetl transform confluence --bundle /tmp/bundle.zip ...
```

## 2. Upload into a **new** Space

The request part chooses the destination. For a new Space you supply only the team; the Space title
and description are read from the bundle and remain editable until confirmation.

```bash
curl -sS -X POST "$API/imports/preflight" -H "$AUTH" \
  -F 'request={"target":{"kind":"new","team_id":"<26-char-team-id>"}};type=application/json' \
  -F 'bundle=@/tmp/bundle.zip;type=application/zip' | jq .
```

Expect `201 Created` and a body like:

```json
{
  "id": "kq3f…",
  "state": "queued_preflight",
  "target": {"kind": "new", "space_id": "j7x…", "team_id": "…", "existed": false},
  "bundle": {
    "version": 2,
    "source": {"organization_id": "", "space_key": "DOCS", "space_name": "Docs"},
    "space_defaults": {"title": "Docs", "description": "Migrated from Confluence space: DOCS"},
    "counts": {
      "pages": 6, "comments": 1, "attachments": 1,
      "restricted_manifest_total": 2, "restricted_emitted_pages": 1, "restricted_manifest_only": 1
    }
  },
  "source_candidates": [],
  "selected_source": {"mode": "new", "import_source_id": "…"},
  "required_acknowledgements": [
    "confirm_new_space_metadata", "page_only_partial_import", "widen_restricted_pages"
  ]
}
```

What to check:

- **Counts match the generator's output.** They are the parsed counts, reconciled against the
  manifest — a disagreement is rejected, not warned about.
- **`target.space_id` is populated even though no Space exists.** The id is pre-generated so
  execution can serialize per target later.
- **`restricted_emitted_pages` vs `restricted_manifest_only`.** Only entries intersecting emitted
  pages count as pages whose access would widen; manifest-only entries are reported separately.
- **`required_acknowledgements`** lists what confirmation will demand. `reimport_existing_pages` is
  deliberately absent: whether anything is a reimport is only known after preflight.

## 3. Upload into an **existing** Space

```bash
curl -sS -X POST "$API/imports/preflight" -H "$AUTH" \
  -F 'request={"target":{"kind":"existing","space_id":"<26-char-space-id>"}};type=application/json' \
  -F 'bundle=@/tmp/bundle.zip;type=application/zip' | jq '{state, target, source_candidates}'
```

Expect `state: "awaiting_source"`, and note that `target.team_id` is the **Space's** team — it is
derived server-side and a `team_id` in the request is rejected for this mode. `source_candidates` is
empty until Space has import sources (created during execution, so it stays empty for now).

## 4. Read back the job and its findings

```bash
export JOB="<job id from the upload response>"

# Job status
curl -sS "$API/imports/$JOB" -H "$AUTH" | jq .

# Everything inspection found, paginated
curl -sS "$API/imports/$JOB/issues?per_page=100" -H "$AUTH" | jq '.items[] | {severity, code, message}'

# Filter by severity or stage
curl -sS "$API/imports/$JOB/issues?severity=warning" -H "$AUTH" | jq '.items[].code'

# Your own jobs, newest first (optionally scoped to a team)
curl -sS "$API/imports?per_page=20" -H "$AUTH" | jq '.items[] | {id, state, create_at}'
```

With `-with-findings` you should see codes such as:

| Code | Meaning |
|---|---|
| `manifest_warning` | A producer warning copied verbatim from the manifest |
| `attachments_not_imported` | The page carries attachment records; none are imported in this release |
| `placeholder_in_text_not_rewritten` | A `{{CONF_…}}` token sits in ordinary text and is left intact |
| `bundle_team_mismatch` | The bundle's advisory team differs from the requested team (never reroutes) |
| `attachment_checksum_not_verified` | The manifest supplied an attachment checksum, which is out of scope |

Note that issue visibility is **actor-only**: another user requesting your job gets `404` (not
`403`), so the endpoint cannot be used to probe for someone else's import.

## 5. Verify the rejection paths

Each mode breaks exactly one contract rule:

```bash
for mode in count-mismatch bad-checksum missing-parent bad-tiptap deep-hierarchy; do
  go run ./server/cmd/genimportbundle -out "/tmp/broken-$mode.zip" -pages 12 -corrupt "$mode" >/dev/null
  printf '%-16s -> ' "$mode"
  curl -sS -o /dev/null -w '%{http_code}\n' -X POST "$API/imports/preflight" -H "$AUTH" \
    -F 'request={"target":{"kind":"new","team_id":"<team-id>"}};type=application/json' \
    -F "bundle=@/tmp/broken-$mode.zip;type=application/zip"
done
```

Expected statuses, and the stable code carried in the response body's message parameters:

| Mode | Status | Why |
|---|---|---|
| `count-mismatch` | 400 | Manifest counts disagree with the emitted lines |
| `bad-checksum` | 400 | `import.jsonl` does not match the manifest digest |
| `missing-parent` | 400 | A page references a parent never emitted |
| `bad-tiptap` | 400 | Page content is not a TipTap `doc` |
| `deep-hierarchy` | 422 | Structurally valid, but deeper than the Docs depth limit of 10 |

A rejected upload creates **no** job — confirm with `GET /imports`. Other things worth trying: a
non-ZIP file, a raw Confluence export, omitting the `request` or `bundle` part, adding an extra
part, and uploading to a Space you are not a member of (`403`).

## 6. What to look for in the server logs

The import paths emit structured, operator-facing lines that never contain page bodies or archive
bytes. The accepted line is logged at **info** (visible without debug logging):

```text
Import upload inspection accepted  job_id=… actor_id=… team_id=… target_kind=new
  target_space_id=… target_space_existed=false state=queued_preflight bundle_sha256=…
  source_space_key=DOCS source_organization_id= pages=6 comments=1 attachments=1
  restricted_emitted_pages=1 restricted_manifest_only=1 inspection_issues=7
```

Rejections are logged at **warn** with the reason and no bundle content:

```text
Import upload rejected: bundle inspection failed  actor_id=… team_id=… target_space_id=…
  bundle_sha256=… error_id=app.import.bundle_invalid.app_error status=400
Import upload rejected: target authorization failed  actor_id=… target_kind=new
  error_id=app.import.target.cannot_create_space.app_error status=403
```

Cross-check `bundle_sha256` against the generator's `archive sha256` line to confirm the bytes the
server hashed are the bytes you sent.

## 7. Confirm nothing was written and nothing was left behind

- **No pages.** `GET /api/v1/spaces/{space_id}/pages` for an existing target is unchanged; a
  new-Space target has no Space yet at all.
- **No temp files.** The upload is streamed to a `0600` file inside a fresh `0700` temp directory and
  both are removed on every return path, success included. Check your temp dir for leftover
  `docs-import-*` directories after a run — there should be none.
- **Staging lives in PostgreSQL**, so nothing the later phases need depends on the node that
  received the upload:

```sql
SELECT State, TargetKind, ProgressTotal, LENGTH(BundleSha256) AS sha_len FROM DOCS_ImportJob;
SELECT COUNT(*) FROM DOCS_ImportStagedPage WHERE JobId = '<job id>';
SELECT Stage, Severity, Code FROM DOCS_ImportIssue WHERE JobId = '<job id>' ORDER BY Ordinal;
```

## Automated equivalents

The same paths are covered by tests, if you would rather not click through:

```bash
go test ./server/importer/...                                   # bundle parsing/validation, pure
go test ./server/ -run TestHandleCreateImport -v                # upload, auth, rejections, staging
go test ./server/ -run 'TestHandleGetImport|TestHandleListImports' -v
```

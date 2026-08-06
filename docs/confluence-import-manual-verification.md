# Manually verifying Confluence bundle import (upload + inspection)

This walks through exercising the import upload/inspection API by hand. It covers what is
implemented today: a bundle can be uploaded, validated, and staged, and its findings can be read
back.

> **No page is written yet.** Upload, source selection, preflight, and confirmation all work, so a job
> runs as far as `queued_import` and waits there: page execution is not implemented. That is the
> expected end state for now — see `implementation-plans/confluence-page-import.md`, phase 5.
>
> **Single node only.** V1 is designed for one importer worker on one application node, and is
> explicitly unsupported on clustered deployments until the HA follow-up lands. The worker goroutine
> processes jobs and runs the hourly maintenance sweep from the same loop, so cleanup never overlaps the
> work whose capacity it reclaims.

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

## 4b. Cancel a job and watch capacity come back

Admission bounds how many jobs and how many staged bytes one user may hold (3 jobs, 512 MiB), so
cancelling is how you give that budget back:

```bash
curl -sS -X POST "$API/imports/$JOB/cancel" -H "$AUTH" | jq '{id, state}'   # 202, state "canceled"
```

Uploading a fourth concurrent bundle returns `429` with `Retry-After`; cancelling one lets the next
upload through. Cancelling does four things worth checking separately:

1. **It records what the bundle held before throwing it away.** Every entity the job has durable input
   for gets an `execution` result row with outcome `not_attempted_canceled` — staged pages, plus
   anything preflight had already classified but that has no staged page of its own (the stale mappings,
   whose ordinals start above the page range). The job's `FinalSummary` counts them. Without this the
   staged rows — the only record of which pages the bundle contained — would be deleted with no outcome
   behind them, and a canceled job's report could not name a single page.
2. **It releases the staged reservation** and deletes the page bodies, while keeping the job, its
   issues, and now its outcomes, so the report still reads back.
3. **It trues the retained reservation down to actual usage.** Admission reserves a full execution's
   worth of report rows up front; a terminal job can never write them, so holding that reservation for
   the ninety-day retention window would lock the user out after roughly twenty upload/cancel cycles
   despite them holding no live job. Repeat the upload/cancel pair thirty times and every upload must
   still return `201`. The final summary is charged before the true-up, so the retained figure is not
   short of the JSONB the job actually holds.
4. **It records the reason.** `ErrorCode` becomes `canceled_by_user` (or `job_expired` from the
   maintenance sweep) and is surfaced as `error.code` on every later read, including the redacted
   projection below.

Cancelling stays available after you lose access to the target Space, but the **response is redacted**
to the same minimal projection `GET` returns: state, error, and timestamps, with no target ids, bundle
counts, or selected-source name. Owning a job authorizes cancelling it, not reading its target.

The hourly maintenance sweep does the same automatically: any pre-execution job past its seven-day
deadline is canceled with `job_expired` (outcomes and true-up included), terminal staged bodies are
purged after seven days, and jobs are deleted after ninety — each step releasing what it held. One pass
also runs at activation, so a restart reclaims anything abandoned while the server was down. Watch for:

```text
Import maintenance pass completed  expired_jobs=1 purged_staged_jobs=0 deleted_jobs=0
  kept_for_compensation_jobs=0 released_staged_bytes=20480 released_retained_bytes=61411520
```

## 4c. Choose an import source (existing Space only)

An **ImportSource** is the local identity a Confluence space's page history is tracked against. It is
what makes a second import of the same space an *update* rather than a duplicate. A new-Space target has
exactly one possible identity and skips this step; an existing-Space target waits in `awaiting_source`
until you pick one, and the worker deliberately does nothing until you do.

`source_candidates` on the job scores existing sources in the Space by organization ID, space key,
display-name similarity, last import time, and mapped-page count. **Nothing is ever selected
automatically** — two Confluence instances can share all of those and still be different sources, so an
automatic match could merge two unrelated page histories.

```bash
# Reuse an existing source, continuing its page history.
curl -sS -X POST "$API/imports/$JOB/source" -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"mode":"existing","import_source_id":"<26-char-source-id>"}' | jq '{state, selected_source}'

# Or start a fresh identity for this space.
curl -sS -X POST "$API/imports/$JOB/source" -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"mode":"new","display_name":"Acme Confluence / DOCS"}' | jq '{state, selected_source}'
```

Expect `202` and `state: "queued_preflight"`. Things worth checking:

- **A new source creates no row yet.** `selected_source.import_source_id` is reserved on the job, but
  `SELECT * FROM DOCS_ImportSource` shows nothing new: an unconfirmed job must not leave an identity
  behind for later jobs to match against. The row appears at execution.
- **A source from another Space returns `404`, not `403`** — the endpoint cannot be used to probe which
  sources exist elsewhere.
- **Selecting twice returns `409`.**
- **Mixing modes returns `400`** (`import_source_id` with `mode: new`, or vice versa).

## 4d. Watch preflight run, then confirm

The worker picks up `queued_preflight` within a couple of seconds. Preflight resolves authors, hashes
every page's source content, compares it against the selected source's mappings, and publishes a plan
all-or-nothing:

```bash
curl -sS "$API/imports/$JOB" -H "$AUTH" | jq '{state, preflight, required_acknowledgements}'
curl -sS "$API/imports/$JOB/preflight-results?per_page=100" -H "$AUTH" \
  | jq '.items[] | {external_id, title, planned_action, overwrite_eligible, structural_changes}'
```

Expect `state: "awaiting_confirmation"` and a `preflight.revision` — a 64-character digest of everything
you were shown. The planned action per page follows the reimport table:

| Source content | Local content | Planned action |
|---|---|---|
| unchanged | unchanged | `noop` |
| changed | unchanged | `update` |
| unchanged | changed | `preserve_local` |
| changed | changed | `conflict` (approvable) |
| no mapping | — | `create` |

Structure is deliberately independent of content. A page moved in Confluence, or moved locally by a user,
emits `source_parent_changed_not_applied` / `local_parent_changed_preserved` and keeps its current
position — it does **not** become a content change. Folding either in would turn a safe body update into a
conflict, or silently undo someone's reorganization.

Pages are `blocked` rather than written when the mapped page was deleted (`mapped_target_missing`), moved
to another Space (`mapped_target_wrong_space`), has no resolvable parent (`parent_mapping_missing`), or
would breach the target's sibling/depth limits. Each carries a message and remediation on the issues
endpoint.

Then confirm, echoing the revision and every acknowledgement the job asked for:

```bash
curl -sS -X POST "$API/imports/$JOB/confirm" -H "$AUTH" -H 'Content-Type: application/json' -d '{
  "preflight_revision": "<preflight.revision>",
  "new_space": {"title": "Imported Docs", "description": "From Confluence"},
  "acknowledgements": {
    "confirm_new_space_metadata": true,
    "page_only_partial_import": true,
    "widen_restricted_pages": true,
    "reimport_existing_pages": true
  },
  "overwrite_conflicts": ["101"]
}' | jq '{state}'
```

Expect `202` and `state: "queued_import"`. The job stops there in this release.

Rejections worth exercising, all before the point of no return:

| Attempt | Result |
|---|---|
| A revision that is not the current one | `409` |
| Omitting an acknowledgement the job listed | `400` naming the missing key |
| An unrecognized acknowledgement key | `400` |
| `new_space` on an existing-Space target, or omitting it for a new one | `400` |
| An `overwrite_conflicts` id that is not a conflict of this job | `400` |
| Confirming twice | `409` |
| Another user confirming | `404` |

**The stale-preflight path is worth seeing.** While a job sits in `awaiting_confirmation`, bump its
source's revision as another import would:

```sql
UPDATE DOCS_ImportSource SET MappingRevision = MappingRevision + 1 WHERE Id = '<source id>';
```

Confirming now returns `409` with `app.import.confirm.preflight_stale_recomputing.app_error` inside the
repository's shared conflict envelope (`{"error": {...}, "current_page": null}`). The job is already back
in `queued_preflight` with its revision, summary, confirmation, and prior plan rows cleared — so the old
plan cannot be confirmed, and the worker publishes a fresh revision within seconds. The browser never
sends hashes: approval carries intent, and the server-owned baselines carry safety.

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

More rejection classes worth trying by hand, all refusals rather than silent repairs:

- **A NUL or invalid UTF-8** anywhere persisted (title, body text, user proposal, `import_labels`,
  and the manifest's `source.space_name`) is rejected with `unstorable_text`/`tiptap_unstorable_text`.
  PostgreSQL cannot store a NUL, and quietly stripping it would alter the user's content behind their
  back.
- **An out-of-contract identifier** — a page id, account id, or space key that is empty, longer than
  512 bytes (255 for space/organization keys), or contains anything outside `[A-Za-z0-9._:@~-]` — is
  rejected so index sizing stays deterministic.
- **Over-long display text** — a `source.space_name` past 255 characters (`space_name_too_long`), or a
  space title past 128 / description past 1024 (`space_text_too_long`). These are stored but not
  indexed, so they are bounded by character count; without the check an over-long value would surface
  as a column or summary write failure rather than a rejection. Both map to **422**.
- **A missing source-namespace mirror** (`space_key_missing`). The producer repeats the space key on
  the `version` line's `source` block, in `space.props.import_source_id`, and in
  `resolve_space_placeholders.space_import_source_id`. All three are required: an absent mirror leaves
  the bundle's own statement of which namespace it belongs to unverified. Drop any one of them from a
  generated `import.jsonl`, re-checksum, and the upload is refused.
- **Over-limit page content** is separated from malformed page content. A document past the body-size,
  node-count, or nesting limit is **422** (`tiptap_body_too_large`, `tiptap_too_many_nodes`,
  `tiptap_too_deep`); one the sanitizer's allowlist refuses is **400**
  (`tiptap_sanitizer_rejected`). The importer's nesting limit is the same as the shared editor
  sanitizer's, so any document the editor stores can also be imported.

A second concurrent upload receives `429` with `Retry-After: 30`: only one bundle inspection runs per
process. Exceeding the per-user, per-target, or global staged-byte budget also returns `429`.

Two things that are deliberately *not* client errors:

- **A failing temp file** (full disk, unwritable temp dir) returns **500**
  `api.import.temp_storage_failed.app_error`, not 400. The transfer copies from the request into the
  temp file, so an error could come from either side; the destination is wrapped to record its own
  failures, and only a read-side failure is reported as the client's.
- **An archive with too many entries** returns **413**. The limit (25 000) is checked by counting actual
  central-directory records before the ZIP reader is constructed, not by reading the trailer's declared
  count — that field is attacker-controlled and Go's reader ignores it, comparing only its low 16 bits,
  so a bundle declaring 0 entries while carrying 65 536 would otherwise sail past. Rejecting before
  construction is what keeps a 250 MiB upload from allocating gigabytes of entry structs.

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

The worker logs each preflight it publishes, with the plan's shape:

```text
Import preflight published  job_id=… actor_id=… target_space_id=… preflight_revision=…
  mapping_revision=1 pages=4 results=4 issues=6 create=2 update=1 noop=0 preserve_local=0
  conflict=1 blocked=0 stale=0
Import source selected  job_id=… actor_id=… target_space_id=… mode=existing import_source_id=…
Import confirmed  job_id=… actor_id=… preflight_revision=… mapping_revision=1 approved_overwrites=1
```

A preflight discarded because its inputs moved says so explicitly rather than failing:

```text
Import preflight discarded: source mappings changed during computation  job_id=… mapping_revision=1
```

## 7. Confirm nothing was written and nothing was left behind

- **No pages.** `GET /api/v1/spaces/{space_id}/pages` for an existing target is unchanged; a
  new-Space target has no Space yet at all.
- **No temp files.** The upload is streamed to a `0600` file inside a fresh `0700` temp directory and
  both are removed on every return path, success included. Check your temp dir for leftover
  `docs-import-*` directories after a run — there should be none.
- **Staging lives in PostgreSQL**, so nothing the later phases need depends on the node that
  received the upload:

```sql
SELECT State, TerminalIntent, ErrorCode, TargetKind, ProgressTotal, StagedBytes,
       RetainedBytes, RetainedIssueBytes, RetainedReservedBytes
  FROM DOCS_ImportJob;
SELECT COUNT(*) FROM DOCS_ImportStagedPage WHERE JobId = '<job id>';
SELECT Ordinal, SourceLine, ExternalId, Restricted FROM DOCS_ImportStagedPage
  WHERE JobId = '<job id>' ORDER BY Ordinal;
-- Preflight's plan, written back onto the staged rows. Every hash here is a reviewed baseline that
-- execution rechecks under locks before applying anything.
SELECT Ordinal, ExternalId, PlannedAction, PlannedPageId, ResolvedUserId, AuthorFallbackReason
  FROM DOCS_ImportStagedPage WHERE JobId = '<job id>' ORDER BY Ordinal;
SELECT Ordinal, ExternalId, PlannedAction, Outcome FROM DOCS_ImportResult
  WHERE JobId = '<job id>' AND Stage = 'preflight' ORDER BY Ordinal;
-- The durable page mappings a reimport compares against. Content hashes and structural baselines are
-- separate columns on purpose, so a preserved local move never reads as a content conflict.
SELECT ExternalId, LocalId, LastAppliedParentId, LastSourceParentExternalId, LastSourceOrdinal
  FROM DOCS_ImportEntity WHERE ImportSourceId = '<source id>';
SELECT Id, DisplayName, ExternalSpaceKey, MappingRevision FROM DOCS_ImportSource;
-- Manifest users are worker input and must survive the request that uploaded them.
SELECT Ordinal, AccountId, MattermostUsername FROM DOCS_ImportManifestUser WHERE JobId = '<job id>';
SELECT Stage, Severity, Code FROM DOCS_ImportIssue WHERE JobId = '<job id>' ORDER BY Ordinal;
-- After a cancel: one durable outcome per staged page, recorded before the pages were deleted.
SELECT Ordinal, ExternalId, ActualAction, Outcome FROM DOCS_ImportResult
  WHERE JobId = '<job id>' AND Stage = 'execution' ORDER BY Ordinal;
-- Admission reserved this job's bytes on the shared singleton row.
-- Reservations must return to zero once every job is canceled, purged, or deleted. A canceled job
-- keeps only RetainedBytes: RetainedReservedBytes is trued up to match it, and the difference comes
-- back to ReservedRetainedBytes here. RetainedIssueBytes is the discretionary (issue-row) share of
-- RetainedBytes, tracked apart so issue writers cannot spend the capacity held for mandatory outcomes.
SELECT * FROM DOCS_ImportCapacity;
-- A job past retention that still holds a pending_compensation attempt is deliberately kept: the
-- attempt row is the only pointer to a channel this import created and must still clean up, and it
-- cascades on job delete. Such a job shows up as kept_for_compensation_jobs in the sweep log.
SELECT JobId, State FROM DOCS_ImportChannelAttempt WHERE State = 'pending_compensation';
```

## Automated equivalents

The same paths are covered by tests, if you would rather not click through:

```bash
go test ./server/importer/...                                   # bundle parsing/validation, pure
go test ./server/ -run TestHandleCreateImport -v                # upload, auth, rejections, staging
go test ./server/ -run 'TestHandleGetImport|TestHandleListImports' -v
go test ./server/importer/ -run TestClassify -v                 # the reimport decision table, pure
go test ./server/ -run 'TestImportPreflight|TestImportSourceSelection|TestImportConfirm' -v
```

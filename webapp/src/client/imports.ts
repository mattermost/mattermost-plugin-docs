// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Client4} from 'mattermost-redux/client';

import type {
    ImportConfirmRequest,
    ImportIssue,
    ImportJobView,
    ImportPreflightResultView,
    ImportSourceSelectionRequest,
    ImportTargetRequest,
    Paginated,
} from 'types/imports';

import {apiUrl, doFetch, RestError} from './rest';

// The typed surface of the Confluence import API: one function per route, taking the request shapes the
// server validates rather than looser conveniences, so a caller cannot easily send a body it will refuse.
//
// Everything JSON goes through the shared `doFetch`. Two things about this API do not fit it, and both are
// handled here rather than by widening the shared helper:
//
//   - the bundle upload is multipart, so its body must not be JSON-serialized and its Content-Type must be
//     left for the browser to set;
//   - only that upload can be rejected for admission, and that rejection carries Retry-After in a *header*,
//     which the shared helper does not surface.
//
// Since the one call that needs response headers is also the one that needs a custom body, it gets its own
// request path and nothing else has to change.

// ImportReportStage names the two reports a job can produce. "final" is the public name for the execution
// stage: a reader is asking for the final outcome, not for the worker phase that produced its rows.
export type ImportReportStage = 'preflight' | 'final';

// ImportAdmissionError is the upload-specific refusal that carries a retry hint.
//
// It extends RestError so callers that branch on RestError/ClientError keep working, and adds the wait the
// server asked for — without which an admission rejection can only invite an immediate retry that fails the
// same way.
export class ImportAdmissionError extends RestError {
    readonly retryAfterSeconds?: number;

    constructor(url: string, status: number, message: string, body: unknown, serverErrorId: string | undefined, retryAfterSeconds?: number) {
        super(url, status, message, body, serverErrorId);
        this.name = 'ImportAdmissionError';
        this.retryAfterSeconds = retryAfterSeconds;
    }
}

// parseRetryAfter reads Retry-After, which this API sends as a whole number of seconds. A date-form value
// (which the HTTP spec also allows) is ignored rather than guessed at, so a nonsense wait is never shown.
const parseRetryAfter = (response: Response): number | undefined => {
    const header = response.headers.get('Retry-After');
    if (!header) {
        return undefined;
    }
    const seconds = Number.parseInt(header, 10);
    return Number.isFinite(seconds) && String(seconds) === header.trim() && seconds >= 0 ? seconds : undefined;
};

// uploadImportBundle posts a bundle and its target, returning the created job.
//
// The target part is appended *first* deliberately: the server reads the request part before the bundle so it
// can authorize the target before spending disk and parser work on the archive. Sending them the other way
// round makes it buffer an upload it is about to reject.
export async function uploadImportBundle(target: ImportTargetRequest, bundle: File): Promise<ImportJobView> {
    const form = new FormData();
    form.append('request', JSON.stringify({target}));
    form.append('bundle', bundle, bundle.name);

    const url = `${apiUrl()}/imports/preflight`;

    // Client4.getOptions supplies the session credentials and CSRF header; the FormData body is passed
    // through untouched, and no Content-Type is set because only the browser knows the multipart boundary.
    const options = Client4.getOptions({method: 'POST'});
    const response = await fetch(url, {...options, body: form});

    if (response.ok) {
        const text = await response.text();
        return (text ? JSON.parse(text) : {}) as ImportJobView;
    }

    let message = `Received status code ${response.status}`;
    let body: unknown;
    let serverErrorId: string | undefined;
    try {
        body = await response.json();
        const data = body as {message?: string; id?: string};
        message = data.message || message;
        serverErrorId = data.id;
    } catch {
        // A non-JSON error body — a proxy's HTML 502, say — still has to produce a usable error, because the
        // status is the actionable part. Keep the status-based message.
    }
    throw new ImportAdmissionError(url, response.status, message, body, serverErrorId, parseRetryAfter(response));
}

// getImportJob reads one job. A job whose target the actor can no longer reach comes back as a deliberately
// minimal projection rather than an error, so callers must tolerate absent target, bundle and source fields.
export const getImportJob = (jobId: string, signal?: AbortSignal): Promise<ImportJobView> =>
    doFetch<ImportJobView>(`/imports/${encodeURIComponent(jobId)}`, {signal});

// listImportJobs returns one page of the actor's own jobs, newest first.
export const listImportJobs = (
    options: {teamId?: string; page?: number; perPage?: number} = {},
    signal?: AbortSignal,
): Promise<Paginated<ImportJobView>> =>
    doFetch<Paginated<ImportJobView>>(`/imports${importQuery({
        team_id: options.teamId,
        page: options.page,
        per_page: options.perPage,
    })}`, {signal});

// selectImportSource records which source identity a job's page history belongs to.
//
// Selection is always explicit — there is no "pick the best candidate" call — because two Confluence
// instances can share an organization id, a space key and a display name while being genuinely different
// sources, and choosing wrong silently merges two unrelated page histories into one mapping set.
export const selectImportSource = (
    jobId: string,
    selection: ImportSourceSelectionRequest,
    signal?: AbortSignal,
): Promise<ImportJobView> =>
    doFetch<ImportJobView>(`/imports/${encodeURIComponent(jobId)}/source`, {
        method: 'POST',
        body: selection,
        signal,
    });

// getImportPreflightResults returns one page of the review table.
export const getImportPreflightResults = (
    jobId: string,
    options: {page?: number; perPage?: number} = {},
    signal?: AbortSignal,
): Promise<Paginated<ImportPreflightResultView>> =>
    doFetch<Paginated<ImportPreflightResultView>>(
        `/imports/${encodeURIComponent(jobId)}/preflight-results${importQuery({
            page: options.page,
            per_page: options.perPage,
        })}`,
        {signal},
    );

// getImportIssues returns one page of a job's findings, optionally filtered by stage and severity.
export const getImportIssues = (
    jobId: string,
    options: {stage?: string; severity?: string; page?: number; perPage?: number} = {},
    signal?: AbortSignal,
): Promise<Paginated<ImportIssue>> =>
    doFetch<Paginated<ImportIssue>>(
        `/imports/${encodeURIComponent(jobId)}/issues${importQuery({
            stage: options.stage,
            severity: options.severity,
            page: options.page,
            per_page: options.perPage,
        })}`,
        {signal},
    );

// confirmImportJob approves a reviewed plan and queues the import.
//
// A 409 here is not necessarily a client bug. If the source's mappings changed since the review, the server
// answers with the stale-preflight code and has *already* returned the job to the preflight queue — so the
// caller waits for a new revision rather than retrying this one. See importErrorCode for reading that code.
export const confirmImportJob = (
    jobId: string,
    request: ImportConfirmRequest,
    signal?: AbortSignal,
): Promise<ImportJobView> =>
    doFetch<ImportJobView>(`/imports/${encodeURIComponent(jobId)}/confirm`, {
        method: 'POST',
        body: request,
        signal,
    });

// cancelImportJob gives a job back.
//
// The returned state distinguishes the two paths: a job with nothing committed comes back already `canceled`,
// while one that may have written pages comes back `terminalizing` and reaches `canceled` once the worker has
// reconciled what it wrote. Neither is an error.
export const cancelImportJob = (jobId: string, signal?: AbortSignal): Promise<ImportJobView> =>
    doFetch<ImportJobView>(`/imports/${encodeURIComponent(jobId)}/cancel`, {method: 'POST', signal});

// importReportUrl is the download URL for a report.
//
// Reports are served as a streamed attachment, so they are handed to the browser as a plain navigation rather
// than fetched into memory: one covers every entity a job touched and can be megabytes.
export const importReportUrl = (jobId: string, stage: ImportReportStage): string =>
    `${apiUrl()}/imports/${encodeURIComponent(jobId)}/report?stage=${stage}`;

// --- reading a failure ---

// The shape of an AppError body, and of the envelope a 409 wraps one in.
type AppErrorBody = {id?: string; message?: string; params?: {Code?: string}};
type ConflictBody = {error?: AppErrorBody};

// appErrorOf digs the AppError out of a RestError's payload.
//
// A 409 nests it one level deeper, inside the conflict envelope every Docs conflict shares. The shared
// request helper reads the id from the top level, so for a conflict it finds nothing — which is why import
// callers must go through here rather than trusting `server_error_id`.
const appErrorOf = (error: unknown): AppErrorBody => {
    if (!(error instanceof RestError) || !error.body || typeof error.body !== 'object') {
        return {};
    }
    if (error.status === 409) {
        const nested = (error.body as ConflictBody).error;
        if (nested && typeof nested === 'object') {
            return nested;
        }
    }
    return error.body as AppErrorBody;
};

// importErrorId returns the stable message id a failure carries, or ''.
//
// The id is the contract. Every refusal the server can produce has one, and it is what a client branches on —
// the message is server-rendered English fit for a fallback, not for logic.
export const importErrorId = (error: unknown): string => appErrorOf(error).id ?? '';

// importErrorCode returns the stable importer/job code, or ''.
//
// Some failures share one message id and distinguish themselves by this code, passed as a message parameter
// because DetailedError is scrubbed before the response is sent: every bundle rejection shares
// app.import.bundle_*.app_error, and a stale confirmation reports preflight_stale_recomputing this way.
export const importErrorCode = (error: unknown): string => appErrorOf(error).params?.Code ?? '';

// PREFLIGHT_STALE_CODE marks a confirmation refused because the reviewed plan no longer describes the source.
// The job is already back in the preflight queue, so the client waits for a new revision.
export const PREFLIGHT_STALE_CODE = 'preflight_stale_recomputing';

// isPreflightStale reports whether a confirmation failed because its plan went stale.
export const isPreflightStale = (error: unknown): boolean =>
    error instanceof RestError && error.status === 409 && importErrorCode(error) === PREFLIGHT_STALE_CODE;

// importQuery renders a query string from defined values only, so an omitted filter is absent rather than
// sent as an empty string the server would reject.
function importQuery(params: Record<string, string | number | undefined>): string {
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
        if (value !== undefined && value !== '') {
            search.set(key, String(value));
        }
    }
    const query = search.toString();
    return query ? `?${query}` : '';
}

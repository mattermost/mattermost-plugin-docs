// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {
    ImportConfirmRequest,
    ImportIssue,
    ImportJobView,
    ImportPreflightResultView,
    ImportSourceSelectionRequest,
    ImportTargetRequest,
    Paginated,
} from 'types/imports';

import {buildQuery, docsApiRoot, docsFetch} from './rest_client';

// The typed surface of the Confluence import API. One function per route, with the request shapes the
// server validates rather than convenience wrappers, so a caller cannot accidentally send a body the
// server will refuse.

// IMPORT_REPORT_STAGES are the two reports a job can produce. "final" is the public name for the
// execution stage: a reader is asking for the final outcome, not for the worker phase that produced it.
export type ImportReportStage = 'preflight' | 'final';

// uploadImportBundle posts a bundle and its target, returning the created job.
//
// The target part is sent *first* on purpose. The server reads the request part before the bundle so it
// can authorize the target before spending disk and parser work on the archive; a body with the parts
// the other way round makes it buffer the upload it was about to reject.
export async function uploadImportBundle(target: ImportTargetRequest, bundle: File): Promise<ImportJobView> {
    const form = new FormData();
    form.append('request', JSON.stringify({target}));
    form.append('bundle', bundle, bundle.name);

    // No Content-Type header: the browser must set it, because only it knows the multipart boundary.
    return docsFetch<ImportJobView>('/imports/preflight', {method: 'POST', body: form});
}

// getImportJob reads one job. A job whose target the actor can no longer reach comes back as a minimal
// projection rather than an error, so callers must tolerate absent target/bundle fields.
export async function getImportJob(jobId: string): Promise<ImportJobView> {
    return docsFetch<ImportJobView>(`/imports/${encodeURIComponent(jobId)}`);
}

// listImportJobs returns one page of the actor's own jobs, newest first.
export async function listImportJobs(options: {
    teamId?: string;
    page?: number;
    perPage?: number;
} = {}): Promise<Paginated<ImportJobView>> {
    const query = buildQuery({
        team_id: options.teamId,
        page: options.page,
        per_page: options.perPage,
    });
    return docsFetch<Paginated<ImportJobView>>(`/imports${query}`);
}

// selectImportSource records which source identity a job's history belongs to.
//
// Selection is always explicit — there is no "pick the best candidate" call — because two Confluence
// instances can look identical while being different sources, and getting it wrong silently merges two
// unrelated page histories.
export async function selectImportSource(
    jobId: string,
    selection: ImportSourceSelectionRequest,
): Promise<ImportJobView> {
    return docsFetch<ImportJobView>(`/imports/${encodeURIComponent(jobId)}/source`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(selection),
    });
}

// getImportPreflightResults returns one page of the review table.
export async function getImportPreflightResults(
    jobId: string,
    options: {page?: number; perPage?: number} = {},
): Promise<Paginated<ImportPreflightResultView>> {
    const query = buildQuery({page: options.page, per_page: options.perPage});
    return docsFetch<Paginated<ImportPreflightResultView>>(
        `/imports/${encodeURIComponent(jobId)}/preflight-results${query}`,
    );
}

// getImportIssues returns one page of a job's findings, optionally filtered.
export async function getImportIssues(
    jobId: string,
    options: {stage?: string; severity?: string; page?: number; perPage?: number} = {},
): Promise<Paginated<ImportIssue>> {
    const query = buildQuery({
        stage: options.stage,
        severity: options.severity,
        page: options.page,
        per_page: options.perPage,
    });
    return docsFetch<Paginated<ImportIssue>>(`/imports/${encodeURIComponent(jobId)}/issues${query}`);
}

// confirmImportJob approves a reviewed plan and queues the import.
//
// A 409 here is not necessarily a client bug: if the source's mappings changed since the review, the
// server returns app.import.confirm.preflight_stale_recomputing.app_error and has *already* returned the
// job to the preflight queue, so the caller should wait for a new revision rather than retrying this one.
export async function confirmImportJob(jobId: string, request: ImportConfirmRequest): Promise<ImportJobView> {
    return docsFetch<ImportJobView>(`/imports/${encodeURIComponent(jobId)}/confirm`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(request),
    });
}

// cancelImportJob gives a job back.
//
// The returned state distinguishes the two paths: a job with nothing committed comes back already
// `canceled`, while one that may have written pages comes back `terminalizing` and reaches `canceled`
// once the worker has reconciled what it wrote. Neither is an error.
export async function cancelImportJob(jobId: string): Promise<ImportJobView> {
    return docsFetch<ImportJobView>(`/imports/${encodeURIComponent(jobId)}/cancel`, {method: 'POST'});
}

// importReportUrl is the download URL for a report.
//
// Reports are served as an attachment and are streamed, so they are handed to the browser as a plain
// navigation rather than fetched into memory — a report covers every entity a job touched and can be
// megabytes.
export function importReportUrl(jobId: string, stage: ImportReportStage): string {
    return `${docsApiRoot()}/imports/${encodeURIComponent(jobId)}/report?stage=${stage}`;
}

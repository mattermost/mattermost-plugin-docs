// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {Client4} from 'mattermost-redux/client';

import {
    cancelImportJob,
    confirmImportJob,
    getImportIssues,
    getImportJob,
    getImportPreflightResults,
    importReportUrl,
    listImportJobs,
    selectImportSource,
    uploadImportBundle,
} from './imports_client';
import {DocsApiError, docsApiRoot} from './rest_client';

const SITE_URL = 'https://mm.example.com';

// jsdom supplies Headers, FormData and File but neither fetch nor Response, so responses are faked
// rather than constructed. The fake exposes exactly what the client reads — ok, status, headers.get and
// text — which also keeps these tests independent of whichever fetch implementation the environment has.
type FakeResponse = Pick<Response, 'ok' | 'status'> & {
    headers: {get(name: string): string | null};
    text(): Promise<string>;
};

const rawResponse = (status: number, body: string, headers: Record<string, string> = {}): FakeResponse => {
    const lower = new Map(Object.entries(headers).map(([key, value]) => [key.toLowerCase(), value]));
    return {
        ok: status >= 200 && status < 300,
        status,
        headers: {get: (name: string) => lower.get(name.toLowerCase()) ?? null},
        text: () => Promise.resolve(body),
    };
};

// jsonResponse mirrors how the server writes one: a JSON body with the status it chose.
const jsonResponse = (status: number, body: unknown, headers: Record<string, string> = {}): FakeResponse =>
    rawResponse(status, JSON.stringify(body), {'Content-Type': 'application/json', ...headers});

const mockFetch = (response: FakeResponse) => {
    const spy = jest.fn().mockResolvedValue(response);
    global.fetch = spy as unknown as typeof fetch;
    return spy;
};

// captureError runs a request that must fail and returns its DocsApiError, narrowed. Catching inline
// widens the value to a union with the success type, which then hides every field worth asserting on.
const captureError = async (promise: Promise<unknown>): Promise<DocsApiError> => {
    try {
        await promise;
    } catch (err) {
        if (err instanceof DocsApiError) {
            return err;
        }
        throw err;
    }
    throw new Error('expected the request to fail, but it resolved');
};

// lastCall returns the url and init the client passed to fetch.
const lastCall = (spy: jest.Mock): {url: string; init: RequestInit} => {
    const [url, init] = spy.mock.calls[spy.mock.calls.length - 1];
    return {url: url as string, init: (init ?? {}) as RequestInit};
};

beforeEach(() => {
    Client4.setUrl(SITE_URL);
});

describe('docsApiRoot', () => {
    // The root is derived from the site URL the webapp was served from rather than hardcoded, so a
    // subpath deployment works.
    it('is built from the site URL and the plugin id', () => {
        expect(docsApiRoot()).toBe(`${SITE_URL}/plugins/${manifest.id}/api/v1`);
    });

    it('follows the site URL when it includes a subpath', () => {
        Client4.setUrl(`${SITE_URL}/mattermost`);
        expect(docsApiRoot()).toBe(`${SITE_URL}/mattermost/plugins/${manifest.id}/api/v1`);
    });
});

describe('request shaping', () => {
    it('sends the credentials and CSRF header every Mattermost browser request needs', async () => {
        const spy = mockFetch(jsonResponse(200, {id: 'job1'}));
        await getImportJob('job1');

        const {url, init} = lastCall(spy);
        expect(url).toBe(`${docsApiRoot()}/imports/job1`);
        expect(init.credentials).toBe('include');
        expect(new Headers(init.headers).get('X-Requested-With')).toBe('XMLHttpRequest');
    });

    it('puts the target part before the bundle so the server can authorize before buffering the archive', async () => {
        const spy = mockFetch(jsonResponse(201, {id: 'job1', state: 'queued_preflight'}));
        const bundle = new File([new Uint8Array([1, 2, 3])], 'bundle.zip', {type: 'application/zip'});

        await uploadImportBundle({kind: 'new', team_id: 'team1'}, bundle);

        const {url, init} = lastCall(spy);
        expect(url).toBe(`${docsApiRoot()}/imports/preflight`);
        expect(init.method).toBe('POST');

        const form = init.body as FormData;
        expect([...form.keys()]).toEqual(['request', 'bundle']);
        expect(form.get('request')).toBe(JSON.stringify({target: {kind: 'new', team_id: 'team1'}}));

        // The browser must set Content-Type for multipart: only it knows the boundary.
        expect(new Headers(init.headers).get('Content-Type')).toBeNull();
    });

    it('omits absent query filters rather than sending empty values', async () => {
        const spy = mockFetch(jsonResponse(200, {items: [], page: 0, per_page: 20, has_more: false}));

        await listImportJobs({teamId: 'team1'});
        expect(lastCall(spy).url).toBe(`${docsApiRoot()}/imports?team_id=team1`);

        await getImportIssues('job1', {severity: 'error', perPage: 100});
        expect(lastCall(spy).url).toBe(`${docsApiRoot()}/imports/job1/issues?severity=error&per_page=100`);

        await getImportPreflightResults('job1');
        expect(lastCall(spy).url).toBe(`${docsApiRoot()}/imports/job1/preflight-results`);
    });

    it('encodes path segments so an id can never break out of its route', async () => {
        const spy = mockFetch(jsonResponse(200, {id: 'x'}));
        await getImportJob('../../spaces/secret');
        expect(lastCall(spy).url).toBe(`${docsApiRoot()}/imports/..%2F..%2Fspaces%2Fsecret`);
    });

    it('sends JSON bodies for source selection and confirmation', async () => {
        const spy = mockFetch(jsonResponse(202, {id: 'job1', state: 'queued_preflight'}));
        await selectImportSource('job1', {mode: 'new', display_name: 'Acme / DOCS'});
        let call = lastCall(spy);
        expect(new Headers(call.init.headers).get('Content-Type')).toBe('application/json');
        expect(JSON.parse(call.init.body as string)).toEqual({mode: 'new', display_name: 'Acme / DOCS'});

        await confirmImportJob('job1', {
            preflight_revision: 'a'.repeat(64),
            acknowledgements: {page_only_partial_import: true},
            overwrite_conflicts: ['101'],
        });
        call = lastCall(spy);
        expect(call.url).toBe(`${docsApiRoot()}/imports/job1/confirm`);
        expect(JSON.parse(call.init.body as string).overwrite_conflicts).toEqual(['101']);
    });

    it('cancels with no body', async () => {
        const spy = mockFetch(jsonResponse(202, {id: 'job1', state: 'canceled'}));
        await cancelImportJob('job1');
        const {init} = lastCall(spy);
        expect(init.method).toBe('POST');
        expect(init.body).toBeUndefined();
    });
});

describe('error handling', () => {
    // Every refusal the server can produce has a stable message id, and that — not the English text —
    // is what a client branches on.
    it('unpacks a plain AppError body', async () => {
        mockFetch(jsonResponse(403, {
            id: 'app.import.target.not_team_member.app_error',
            message: 'You are not a member of that team.',
            status_code: 403,
        }));

        await expect(getImportJob('job1')).rejects.toMatchObject({
            name: 'DocsApiError',
            status: 403,
            id: 'app.import.target.not_team_member.app_error',
        });
    });

    // A 409 nests its AppError one level deeper, inside the shared conflict envelope every conflict
    // uses. Reading it as a bare AppError would lose the id entirely.
    it('unpacks the shared conflict envelope a 409 carries', async () => {
        mockFetch(jsonResponse(409, {
            error: {
                id: 'app.import.confirm.preflight_stale_recomputing.app_error',
                message: 'The reviewed plan is stale.',
                status_code: 409,
                params: {Code: 'preflight_stale_recomputing'},
            },
            current_page: null,
        }));

        const err = await captureError(confirmImportJob('job1', {
            preflight_revision: 'a'.repeat(64),
            acknowledgements: {},
        }));

        expect(err.isConflict).toBe(true);
        expect(err.id).toBe('app.import.confirm.preflight_stale_recomputing.app_error');
        expect(err.code).toBe('preflight_stale_recomputing');
    });

    // Bundle rejections all share one message id and distinguish themselves by the stable importer code
    // carried as a message parameter, because DetailedError is scrubbed before the response is sent.
    it('surfaces the stable importer code from a rejected bundle', async () => {
        mockFetch(jsonResponse(422, {
            id: 'app.import.bundle_content_not_processable.app_error',
            message: 'The bundle exceeds a Docs limit.',
            status_code: 422,
            params: {Code: 'too_many_pages'},
        }));

        const bundle = new File([new Uint8Array([1])], 'bundle.zip');
        const err = await captureError(uploadImportBundle({kind: 'new', team_id: 'team1'}, bundle));

        expect(err.status).toBe(422);
        expect(err.code).toBe('too_many_pages');
    });

    // An admission rejection has to tell the user when to try again; inviting an immediate retry would
    // just fail the same way.
    it('reads Retry-After from a 429', async () => {
        mockFetch(jsonResponse(
            429,
            {id: 'app.import.admission_exhausted.app_error', message: 'Too many imports.', status_code: 429},
            {'Retry-After': '120'},
        ));

        const bundle = new File([new Uint8Array([1])], 'bundle.zip');
        const err = await captureError(uploadImportBundle({kind: 'new', team_id: 'team1'}, bundle));

        expect(err.isTooManyRequests).toBe(true);
        expect(err.retryAfterSeconds).toBe(120);
    });

    it('ignores an unparseable Retry-After rather than reporting a nonsense wait', async () => {
        mockFetch(jsonResponse(429, {id: 'x', message: 'y'}, {'Retry-After': 'Wed, 21 Oct 2015 07:28:00 GMT'}));
        const err = await captureError(getImportJob('job1'));
        expect(err.retryAfterSeconds).toBeUndefined();
    });

    // A proxy returning an HTML 502 must not surface as a JSON syntax error: the status is the
    // actionable part and the caller would lose it.
    it('still produces a usable error for a non-JSON failure body', async () => {
        mockFetch(rawResponse(502, '<html>Bad Gateway</html>', {'Content-Type': 'text/html'}));

        const err = await captureError(getImportJob('job1'));
        expect(err.status).toBe(502);
        expect(err.id).toBe('');
        expect(err.message).toContain('502');
    });

    // Actor-only visibility means another user's job reads as absent, not forbidden — so a 404 cannot be
    // distinguished from "deleted", by design.
    it('reports a 404 as not found', async () => {
        mockFetch(jsonResponse(404, {id: 'app.store.not_found.app_error', message: 'Not found.', status_code: 404}));
        const err = await captureError(getImportJob('job1'));
        expect(err.isNotFound).toBe(true);
    });
});

describe('report downloads', () => {
    // Reports are streamed attachments, so they are handed to the browser as a URL rather than fetched
    // into memory: one covers every entity a job touched and can be megabytes.
    it('builds the download URL for each stage', () => {
        expect(importReportUrl('job1', 'preflight')).toBe(`${docsApiRoot()}/imports/job1/report?stage=preflight`);
        expect(importReportUrl('job1', 'final')).toBe(`${docsApiRoot()}/imports/job1/report?stage=final`);
    });
});

describe('success bodies', () => {
    it('parses a job view', async () => {
        mockFetch(jsonResponse(200, {
            id: 'job1',
            state: 'awaiting_confirmation',
            progress: {phase: 'computing_actions', current: 3, total: 3},
            target: {kind: 'new', team_id: 'team1', existed: false},
            source_candidates: [],
            required_acknowledgements: ['confirm_new_space_metadata'],
            preflight: {stage: 'preflight', generated_at: 1, revision: 'b'.repeat(64), counts: {actions: {create: 3}}},
        }));

        const job = await getImportJob('job1');
        expect(job.state).toBe('awaiting_confirmation');
        expect(job.preflight?.revision).toHaveLength(64);
        expect(job.required_acknowledgements).toEqual(['confirm_new_space_metadata']);
    });

    it('resolves with no payload when a success body is empty', async () => {
        mockFetch(rawResponse(200, ''));
        await expect(getImportJob('job1')).resolves.toBeUndefined();
    });
});

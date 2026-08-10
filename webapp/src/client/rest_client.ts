// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {Client4} from 'mattermost-redux/client';

// The plugin's REST root. Client4.getUrl() supplies the site URL the webapp was served from, so this
// works behind a subpath deployment where a hardcoded absolute path would not.
export const docsApiRoot = (): string => `${Client4.getUrl()}/plugins/${manifest.id}/api/v1`;

// DocsApiError is a failed Docs API response, unpacked into something a caller can branch on.
//
// The `id` is the load-bearing field. Every refusal the server can produce has a stable message id —
// app.import.confirm.preflight_stale_recomputing.app_error, app.import.admission_exhausted.app_error and
// so on — and those are the contract. `message` is server-rendered English suitable for a fallback, not
// something to key behaviour off.
export class DocsApiError extends Error {
    readonly status: number;
    readonly id: string;

    // code is the stable importer/job code the server passes as a message parameter for the failures
    // where the message id alone is too coarse: bundle rejections all share one id and distinguish
    // themselves by code, as do stale-preflight conflicts.
    readonly code: string;

    // retryAfterSeconds is populated from the Retry-After header on a 429, so an admission rejection can
    // tell the user when to try again instead of inviting an immediate retry that will also fail.
    readonly retryAfterSeconds?: number;

    constructor(init: {status: number; id: string; message: string; code?: string; retryAfterSeconds?: number}) {
        super(init.message);
        this.name = 'DocsApiError';
        this.status = init.status;
        this.id = init.id;
        this.code = init.code ?? '';
        this.retryAfterSeconds = init.retryAfterSeconds;
    }

    // isNotFound covers both "gone" and "not yours": import job visibility is actor-only and another
    // user's job reads as 404 rather than 403, deliberately, so these are indistinguishable by design.
    get isNotFound(): boolean {
        return this.status === 404;
    }

    get isConflict(): boolean {
        return this.status === 409;
    }

    get isTooManyRequests(): boolean {
        return this.status === 429;
    }
}

// The shape of a Mattermost AppError body.
type AppErrorBody = {
    id?: string;
    message?: string;
    status_code?: number;

    // The server passes stable codes through as message parameters rather than in DetailedError, which
    // is scrubbed before it is sent.
    params?: {Code?: string};
};

// Every 409 carries this envelope rather than a bare AppError, so a client parses a conflict the same
// way whichever endpoint produced it. current_page is an optional shortcut for page conflicts and is
// null for the import routes.
type ConflictBody = {
    error?: AppErrorBody;
};

// parseErrorBody pulls the AppError out of either shape, since a 409 nests it one level deeper.
const parseErrorBody = (status: number, body: unknown): AppErrorBody => {
    if (!body || typeof body !== 'object') {
        return {};
    }
    if (status === 409) {
        const nested = (body as ConflictBody).error;
        if (nested && typeof nested === 'object') {
            return nested;
        }
    }
    return body as AppErrorBody;
};

// parseRetryAfter reads the Retry-After header, which the server sends as a whole number of seconds.
const parseRetryAfter = (response: Response): number | undefined => {
    const header = response.headers.get('Retry-After');
    if (!header) {
        return undefined;
    }
    const seconds = Number.parseInt(header, 10);
    return Number.isFinite(seconds) && seconds >= 0 ? seconds : undefined;
};

// docsFetch performs one Docs API request and returns the parsed JSON body, throwing DocsApiError for
// anything that is not a success.
//
// It goes through fetch rather than Client4.doFetch because the import upload is a multipart body with
// a caller-supplied FormData, and because these responses need their headers (Retry-After) and their
// conflict envelope unpacked — none of which the shared helper exposes. The headers below are what
// Mattermost requires of a browser client: the session cookie plus the CSRF-defeating
// X-Requested-With, which is why credentials are always sent.
export async function docsFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set('X-Requested-With', 'XMLHttpRequest');

    const response = await fetch(`${docsApiRoot()}${path}`, {
        ...init,
        credentials: 'include',
        headers,
    });

    if (!response.ok) {
        throw await toDocsApiError(response);
    }

    // 202 and 200 bodies are JSON for every route the client uses; a genuinely empty body still has to
    // resolve rather than throw, so an unparseable success is treated as "no payload".
    const text = await response.text();
    if (!text) {
        return undefined as T;
    }
    return JSON.parse(text) as T;
}

// toDocsApiError converts a failed response into a DocsApiError, tolerating a non-JSON body.
//
// A failure that cannot be parsed still has to produce a usable error: a proxy returning an HTML 502
// must not surface as a JSON syntax error, because the status is the actionable part and the caller
// would lose it.
async function toDocsApiError(response: Response): Promise<DocsApiError> {
    let body: unknown;
    try {
        body = JSON.parse(await response.text());
    } catch {
        body = undefined;
    }
    const appErr = parseErrorBody(response.status, body);
    return new DocsApiError({
        status: response.status,
        id: appErr.id ?? '',
        message: appErr.message ?? `Request failed with status ${response.status}`,
        code: appErr.params?.Code,
        retryAfterSeconds: parseRetryAfter(response),
    });
}

// buildQuery renders a query string from defined values only, so an omitted filter is absent rather
// than sent as an empty string the server would reject.
export function buildQuery(params: Record<string, string | number | boolean | undefined>): string {
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
        if (value !== undefined && value !== '') {
            search.set(key, String(value));
        }
    }
    const query = search.toString();
    return query ? `?${query}` : '';
}

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';

import {ClientError} from '@mattermost/client';

import {Client4} from 'mattermost-redux/client';

// The host site URL, including any configured subpath. Deferred to call time
// because the host configures the client after plugin bundles load. The browser
// basename fallback also keeps links absolute if this bundled client has not yet
// received that configuration.
export const siteRoot = (): string => {
    const basename = (window as unknown as {basename?: string}).basename || '/';
    return new URL(Client4.getUrl() || basename, `${window.location.origin}/`).href.replace(/\/$/, '');
};

const apiUrl = (): string => `${siteRoot()}/plugins/${manifest.id}/api/v1`;

type FetchOptions = {
    method: string;
    body?: string;
    headers?: Record<string, string>;
    signal?: AbortSignal;
};

/**
 * A failed Docs API call. Extends ClientError so existing callers keep matching
 * on `instanceof ClientError` and `status_code`, and adds the two things an
 * AppError body carries that ClientError drops: the raw parsed payload, and a
 * plain `status`. The payload matters for endpoints that answer an error with
 * data — draft publish returns the current page alongside its 409.

 */
export class RestError extends ClientError {
    status: number;
    body: unknown;

    constructor(url: string, status: number, message: string, body: unknown, serverErrorId?: string) {
        super(siteRoot(), {message, status_code: status, url, server_error_id: serverErrorId});
        this.name = 'RestError';
        this.status = status;
        this.body = body;
    }
}

// Single fetch idiom shared by every Docs API call. Client4.getOptions injects
// the session credentials and CSRF header the server expects (it reads the
// platform-supplied Mattermost-User-Id header), so this never hand-rolls auth.
// Server errors are JSON `AppError`s ({message, status_code}); non-OK responses
// are normalized into RestError. An aborted request
// rejects with fetch's own `AbortError` DOMException instead, so callers can tell
// "I cancelled this" apart from "the server said no".
async function request<T>(url: string, options: FetchOptions): Promise<T> {
    const response = await fetch(url, Client4.getOptions(options));

    if (response.ok) {
        // Actions like DELETE return {"status":"OK"}; callers that expect no
        // payload type this as void and ignore it.
        const text = await response.text();
        return (text ? JSON.parse(text) : {}) as T;
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
        // Non-JSON error body — keep the status-based message.
    }
    throw new RestError(url, response.status, message, body, serverErrorId);
}

export const restGet = <T>(url: string, signal?: AbortSignal): Promise<T> =>
    request<T>(url, {method: 'GET', signal});

export const restPost = <T>(url: string, body: unknown, signal?: AbortSignal): Promise<T> =>
    request<T>(url, {method: 'POST', body: JSON.stringify(body), headers: {'Content-Type': 'application/json'}, signal});

export const restPut = <T>(url: string, body: unknown, signal?: AbortSignal): Promise<T> =>
    request<T>(url, {method: 'PUT', body: JSON.stringify(body), headers: {'Content-Type': 'application/json'}, signal});

export const restPatch = <T>(url: string, body: unknown, signal?: AbortSignal): Promise<T> =>
    request<T>(url, {method: 'PATCH', body: JSON.stringify(body), headers: {'Content-Type': 'application/json'}, signal});

export const restDelete = <T>(url: string, signal?: AbortSignal): Promise<T> =>
    request<T>(url, {method: 'DELETE', signal});

type DoFetchOptions = {
    method?: string;
    body?: unknown;
    signal?: AbortSignal;
};

/**
 * Path-relative form of the request helpers: `path` is resolved against the
 * plugin's API root and `body` is serialized here. Client modules that describe a
 * whole endpoint family (see `client/drafts.ts`) read better this way than
 * building an absolute URL per call.
 */
export const doFetch = <T>(path: string, {method = 'GET', body, signal}: DoFetchOptions = {}): Promise<T> =>
    request<T>(`${apiUrl()}${path}`, {
        method,
        signal,
        ...(body === undefined ? {} : {body: JSON.stringify(body), headers: {'Content-Type': 'application/json'}}),
    });

type Paginated<T> = {
    items: T[];
    page: number;
    per_page: number;
    has_more: boolean;
};

const PER_PAGE = 100;
const MAX_PAGES = 1000;

type PaginationLimitCause<T> = {
    code: 'pagination_limit';
    items: T[];
};

export type PaginationLimitError<T> = Error & {
    cause: PaginationLimitCause<T>;
};

export const isPaginationLimitError = (error: unknown): error is PaginationLimitError<unknown> =>
    error instanceof Error && (error.cause as PaginationLimitCause<unknown>)?.code === 'pagination_limit';

// Follows the server's {items, page, per_page, has_more} envelope across pages
// and returns the flattened list. The page cap is a runaway-loop backstop, not
// an expected limit.
export async function listAll<T>(path: (query: string) => string, signal?: AbortSignal): Promise<T[]> {
    const out: T[] = [];
    for (let page = 0; page < MAX_PAGES; page++) {
        // eslint-disable-next-line no-await-in-loop
        const res = await restGet<Paginated<T>>(path(`page=${page}&per_page=${PER_PAGE}`), signal);
        out.push(...res.items);
        if (!res.has_more) {
            return out;
        }
    }
    throw new Error(`Docs: pagination exceeded the ${MAX_PAGES}-page safety limit`, {
        cause: {
            code: 'pagination_limit',
            items: out,
        } satisfies PaginationLimitCause<T>,
    });
}

export {apiUrl};

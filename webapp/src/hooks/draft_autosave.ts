// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch} from 'hooks/redux';
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';

import {saveDraft} from 'store/actions';

import type {Draft, DraftPatch} from 'types/drafts';

import {publishAutosaveStatus} from './autosave_status';
import {registerPendingSave} from './pending_saves';
import {useLatest} from './utils';

export const AUTOSAVE_DEBOUNCE_MS = 1000;

export type AutosaveStatus = 'saved' | 'saving' | 'unsaved';

type Options = {
    spaceId: string;
    pageId: string;

    enabled: boolean;

    baseEditAt?: number;

    onSaved?: (draft: Draft) => void;
    onError?: (error: unknown) => void;
};

export type DraftAutosave = {
    status: AutosaveStatus;

    queue: (patch: DraftPatch) => void;

    flush: () => Promise<boolean>;

    cancel: () => void;
};

type Pending = {
    spaceId: string;
    pageId: string;

    baseEditAt?: number;
    patch: DraftPatch;
};

export function useDraftAutosave({spaceId, pageId, enabled, baseEditAt, onSaved, onError}: Options): DraftAutosave {
    const dispatch = useAppDispatch();
    const [status, setStatus] = useState<AutosaveStatus>('saved');

    const pendingRef = useRef<Pending | null>(null);
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const abortRef = useRef<AbortController | null>(null);
    const chainRef = useRef<Promise<boolean>>(Promise.resolve(true));

    const generationRef = useRef(0);

    const latest = useLatest({spaceId, pageId, enabled, baseEditAt, onSaved, onError});

    const clearTimer = useCallback(() => {
        if (timerRef.current !== null) {
            clearTimeout(timerRef.current);
            timerRef.current = null;
        }
    }, []);

    const doWrite = useCallback(async (force: boolean): Promise<boolean> => {
        const entry = pendingRef.current;
        const {enabled: on, onSaved: saved, onError: failed} = latest.current;
        if (!entry || (!on && !force)) {
            return true;
        }
        const baseline = entry.baseEditAt;

        pendingRef.current = null;
        const generation = generationRef.current;
        const controller = new AbortController();
        abortRef.current = controller;
        setStatus('saving');

        try {
            const body: DraftPatch = baseline ? {...entry.patch, base_edit_at: baseline} : entry.patch;
            const draft = await dispatch(saveDraft(entry.spaceId, entry.pageId, body, controller.signal));
            if (generation !== generationRef.current) {
                return false;
            }
            saved?.(draft);

            setStatus(pendingRef.current ? 'unsaved' : 'saved');
            return true;
        } catch (error) {
            if (generation !== generationRef.current || controller.signal.aborted) {
                return false;
            }

            const queuedSince = pendingRef.current as Pending | null;
            const sameTarget = !queuedSince ||
                (queuedSince.spaceId === entry.spaceId && queuedSince.pageId === entry.pageId);

            if (sameTarget) {
                pendingRef.current = {
                    ...entry,
                    patch: {...entry.patch, ...(queuedSince?.patch ?? {})},
                };
            }
            setStatus('unsaved');
            failed?.(error);
            return false;
        } finally {
            if (abortRef.current === controller) {
                abortRef.current = null;
            }
        }
    }, [latest, dispatch]);

    const write = useCallback((force = false): Promise<boolean> => {
        const run = () => doWrite(force);
        const next = chainRef.current.then(run, run);
        chainRef.current = next;
        return next;
    }, [doWrite]);

    const queue = useCallback((patch: DraftPatch) => {
        const {spaceId: space, pageId: page, baseEditAt: baseline} = latest.current;
        const prior = pendingRef.current?.spaceId === space && pendingRef.current?.pageId === page ? pendingRef.current.patch : {};

        pendingRef.current = {spaceId: space, pageId: page, baseEditAt: baseline, patch: {...prior, ...patch}};
        setStatus('unsaved');
        clearTimer();
        timerRef.current = setTimeout(() => {
            timerRef.current = null;

            write();
        }, AUTOSAVE_DEBOUNCE_MS);
    }, [clearTimer, write, latest]);

    const flush = useCallback((): Promise<boolean> => {
        clearTimer();
        return write();
    }, [clearTimer, write]);

    const cancel = useCallback(() => {
        generationRef.current += 1;
        clearTimer();
        pendingRef.current = null;
        abortRef.current?.abort();
        abortRef.current = null;
        setStatus('saved');
    }, [clearTimer]);

    useEffect(() => registerPendingSave(flush), [flush]);

    useEffect(() => {
        publishAutosaveStatus(enabled ? status : null);
    }, [enabled, status]);

    useEffect(() => () => publishAutosaveStatus(null), []);

    useEffect(() => () => {
        clearTimer();
        write(true);
    }, [clearTimer, write, spaceId, pageId]);

    return useMemo(() => ({status, queue, flush, cancel}), [status, queue, flush, cancel]);
}

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getPageActiveEditors} from 'client/drafts';
import {subscribeToPagePresence} from 'client/presence_events';
import {useEffect, useMemo, useState} from 'react';

import type {PageActiveEditors} from 'types/drafts';

export function usePagePresence(spaceId: string, pageId: string, currentUserId: string): string[] {
    const [snapshot, setSnapshot] = useState<PageActiveEditors | null>(null);

    const [now, setNow] = useState(() => Date.now());

    useEffect(() => {
        const controller = new AbortController();
        setSnapshot(null);

        getPageActiveEditors(spaceId, pageId, controller.signal).
            then((next) => {
                if (controller.signal.aborted) {
                    return;
                }
                setSnapshot((current) => (current && current.snapshot_at >= next.snapshot_at ? current : next));
            }).
            catch(() => {
            });

        return () => controller.abort();
    }, [spaceId, pageId]);

    useEffect(() => subscribeToPagePresence((event) => {
        if (event.page_id !== pageId) {
            return;
        }

        setSnapshot((current) => {
            if (current && event.snapshot_at < current.snapshot_at) {
                return current;
            }
            return {
                active_editors: event.active_editors,
                snapshot_at: event.snapshot_at,
                active_timeout_ms: event.active_timeout_ms,
            };
        });
    }), [pageId]);

    useEffect(() => {
        if (!snapshot || snapshot.active_timeout_ms <= 0 || snapshot.active_editors.length === 0) {
            return undefined;
        }

        const expiresIn = (snapshot.snapshot_at + snapshot.active_timeout_ms) - Date.now();
        if (expiresIn <= 0) {
            return undefined;
        }
        const timer = setTimeout(() => setNow(Date.now()), expiresIn);
        return () => clearTimeout(timer);
    }, [snapshot]);

    return useMemo(() => {
        if (!snapshot) {
            return [];
        }
        if (snapshot.active_timeout_ms > 0 && now - snapshot.snapshot_at > snapshot.active_timeout_ms) {
            return [];
        }

        return snapshot.active_editors.filter((id) => id !== currentUserId);
    }, [snapshot, now, currentUserId]);
}

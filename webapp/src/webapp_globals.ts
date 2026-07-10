// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {History} from 'history';
import type {Action} from 'redux';

// Hand-typed view of the API the host web app attaches to `window` for plugins
// (core's plugins/export.ts). The host guarantees these at runtime; anything
// missing degrades to a no-op.
//
// TRANSITION-MIGRATION: the modal contract below now lives in core as
// @mattermost/shared/types/global (WindowShared, PublishedModalUtils,
// PublishedModalId, PublishedModalProps). Our pinned @mattermost/shared release
// doesn't export it yet, so it's mirrored here. Replace this slice with those
// imports once the dependency is bumped to a version that ships types/global —
// which also lets openModalById/dialogProps be typed per-modal instead of loose.
// browserHistory has not migrated into WindowShared yet, so it stays here too.

type PublishedModalId = 'user_settings' | 'invitation' | 'team_settings' | 'team_members' | 'leave_team';

type PublishedModalUtils = {

    // The published shared contract types this `void`, but the runtime returns a
    // MODAL_OPEN Redux action the caller dispatches on the (shared) core store.
    openModalById: (modalId: PublishedModalId, dialogProps?: Record<string, unknown>) => Action | undefined;

    // Reports whether the running web app actually publishes this modal id. Takes
    // any string so a newer plugin can probe an id an older host doesn't publish.
    canOpenModalId: (modalId: string) => boolean;
};

type WebappUtils = {
    browserHistory?: History;
    modals?: Partial<PublishedModalUtils>;
};

const webappUtils = (): WebappUtils => (window as unknown as {WebappUtils?: WebappUtils}).WebappUtils ?? {};

export function getBrowserHistory(): History | undefined {
    return webappUtils().browserHistory;
}

// Whether the running host publishes this modal id (false on hosts predating the
// opener), so callers can gate before opening.
export function hostCanOpenModal(modalId: string): boolean {
    return webappUtils().modals?.canOpenModalId?.(modalId) ?? false;
}

// The Redux action that opens a published core modal, or undefined when the host
// build predates the opener (callers no-op instead of crashing).
export function hostOpenModalAction(modalId: PublishedModalId, dialogProps?: Record<string, unknown>): Action | undefined {
    return webappUtils().modals?.openModalById?.(modalId, dialogProps);
}

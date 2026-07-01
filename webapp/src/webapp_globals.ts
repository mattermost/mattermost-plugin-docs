// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {History} from 'history';
import type {Action} from 'redux';

// Hand-typed view of the API the host web app attaches to `window` for plugins
// (core's plugins/export.ts). Typed here the way the plugin registry is — the
// host guarantees these at runtime, and anything missing degrades to a no-op.

type WebappUtils = {
    browserHistory?: History;

    // Opens a core modal from the host's published allowlist by its id.
    openModalById?: (modalId: string, dialogProps?: Record<string, unknown>) => Action;
};

const webappUtils = (): WebappUtils => (window as unknown as {WebappUtils?: WebappUtils}).WebappUtils ?? {};

export function getBrowserHistory(): History | undefined {
    return webappUtils().browserHistory;
}

// Returns the Redux action that opens a published core modal, or undefined when
// the host build predates the opener (so callers no-op instead of crashing).
export function hostOpenModalAction(modalId: string, dialogProps?: Record<string, unknown>): Action | undefined {
    return webappUtils().openModalById?.(modalId, dialogProps);
}

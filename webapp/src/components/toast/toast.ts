// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Toast} from '@base-ui-components/react/toast';
import type {ReactNode} from 'react';

export type DocsToastVariant = 'success' | 'error' | 'warning' | 'info';

export type DocsToastOptions = {
    description?: ReactNode;

    /** Milliseconds before auto-dismiss; `0` keeps the toast until dismissed. */
    timeout?: number;
    priority?: 'low' | 'high';
    onClose?: () => void;
};

export type DocsToastApi = {
    success: (title: ReactNode, options?: DocsToastOptions) => string;
    error: (title: ReactNode, options?: DocsToastOptions) => string;
    warning: (title: ReactNode, options?: DocsToastOptions) => string;
    info: (title: ReactNode, options?: DocsToastOptions) => string;
    dismiss: (id: string) => void;
};

type AddToast = (options: DocsToastOptions & {title: ReactNode; type: DocsToastVariant}) => string;
type CloseToast = (id: string) => void;

// Failures are announced urgently by default; the rest stay polite.
const DEFAULT_PRIORITY: Record<DocsToastVariant, 'low' | 'high'> = {
    success: 'low',
    error: 'high',
    warning: 'high',
    info: 'low',
};

const createToastApi = (add: AddToast, close: CloseToast): DocsToastApi => {
    const show = (type: DocsToastVariant) => (title: ReactNode, options?: DocsToastOptions) => add({
        ...options,
        title,
        type,
        priority: options?.priority ?? DEFAULT_PRIORITY[type],
    });

    return {
        success: show('success'),
        error: show('error'),
        warning: show('warning'),
        info: show('info'),
        dismiss: close,
    };
};

/**
 * The Docs product's toast manager. `<DocsToaster/>` binds to it; prefer the
 * `toast` API over using this directly.
 */
export const docsToastManager = Toast.createToastManager();

/**
 * Imperative toast API usable anywhere — actions, thunks, event handlers — with
 * no hook and no React context. Requires `<DocsToaster/>` to be mounted (it is,
 * at the Docs root).
 */
export const toast: DocsToastApi = createToastApi(
    (options) => docsToastManager.add(options),
    (id) => docsToastManager.close(id),
);

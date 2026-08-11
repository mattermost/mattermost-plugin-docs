// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {closeAllDocsModals, closeDocsModal, openDocsModal} from './modal_store';

const api = {
    open: openDocsModal,
    close: closeDocsModal,
    closeAll: closeAllDocsModals,
};

export type DocsModalsApi = typeof api;

/** Hook flavour of {@link openDocsModal} for components that prefer hooks. */
export const useDocsModals = (): DocsModalsApi => api;

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from 'manifest';
import type {Reducer} from 'redux';
import {DOCS_BASE_URL, DOCS_SWITCHER_LINK_URL} from 'routing/paths';

import reducer from 'store/reducer';

import DocsRootLazy from 'components/docs_root/docs_root_lazy';
import DocsSettingsButton from 'components/docs_settings_button/docs_settings_button';

import type {PluginRegistry} from 'types/mattermost-webapp';

// Compass glyph for the product-switcher icon. The host resolves this name
// through its glyph map and renders it at size 24 (and accent-colored in the
// switcher menu), the same path the built-in products use. There is no
// `product-docs` glyph yet, so a stock document glyph stands in.
const SWITCHER_ICON = 'file-text-outline';

const DocsHeaderCentre = () => null;

export default class Plugin {
    public async initialize(registry: PluginRegistry) {
        registry.registerTranslations({
            getTranslationsForLocale: (locale: string) => {
                try {
                    return require(`../i18n/${locale}.json`); // eslint-disable-line global-require
                } catch {
                    return {};
                }
            },
        });

        // The host's registerReducer type is generic over UnknownAction; ours
        // only cares about its own action types and safely no-ops otherwise.
        // Data loads when the product mounts (see useBootstrapDocs), not here —
        // init must not fetch (no UI is shown yet, and the user may be logged
        // out).
        registry.registerReducer(reducer as Reducer);

        registry.registerProduct({
            baseURL: DOCS_BASE_URL,
            switcherIcon: SWITCHER_ICON,
            switcherText: 'Docs',
            switcherLinkURL: DOCS_SWITCHER_LINK_URL,
            mainComponent: DocsRootLazy,
            headerCentreComponent: DocsHeaderCentre,
            headerRightComponent: DocsSettingsButton,
            showTeamSidebar: true,
            isTeamScoped: true,
        });
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());

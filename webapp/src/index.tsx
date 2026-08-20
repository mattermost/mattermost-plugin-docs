// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {publishPagePresence} from 'client/presence_events';
import type {PagePresenceEvent} from 'client/presence_events';
import manifest from 'manifest';
import type {Reducer, Store} from 'redux';
import {DOCS_BASE_URL, DOCS_SWITCHER_LINK_URL} from 'routing/paths';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {SpaceTypes} from 'store/action_types';
import {fetchSpace, fetchSpaceMembers} from 'store/actions';
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

const PAGE_PRESENCE_EVENT = `custom_${manifest.id}_page_presence_updated`;

// The events the server publishes so a client learns its own access to a space changed without a
// reload. The server sends the permissions one directly to the affected user precisely because
// nothing else tells them; a space's other members get the add/remove pair on the space's channel.
//
// space_updated belongs here for the same reason the per-member events do: it is the only signal
// that a space's own default permission set or its view access changed, and both move every
// member's effective permissions without any member row changing. Omitting it left the two halves
// of one feature disagreeing — a per-member grant refreshed live, a space-default change did not.
const SPACE_ACCESS_EVENTS = [
    `custom_${manifest.id}_space_member_added`,
    `custom_${manifest.id}_space_member_permissions_updated`,
    `custom_${manifest.id}_space_updated`,
];

// Handled apart from the re-resolve set: see the handler for why a removal cannot be one.
const SPACE_MEMBER_REMOVED_EVENT = `custom_${manifest.id}_space_member_removed`;

type SpaceAccessEvent = {space_id: string; user_id?: string};

export default class Plugin {
    public async initialize(registry: PluginRegistry, store: Store) {
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

        registry.registerWebSocketEventHandler<PagePresenceEvent>(PAGE_PRESENCE_EVENT, (msg) => {
            publishPagePresence(msg.data);
        });

        // Re-resolve the space rather than patching a field from the payload: the payload names
        // which space changed, not what the caller may now do, and the answer to that is the
        // server's to give. fetchSpace refreshes the same entry every permission-gated affordance
        // reads, so one dispatch settles them all.
        for (const event of SPACE_ACCESS_EVENTS) {
            registry.registerWebSocketEventHandler<SpaceAccessEvent>(event, (msg) => {
                const spaceId = msg.data?.space_id;
                if (spaceId) {
                    store.dispatch(fetchSpace(spaceId) as never);
                }
            });
        }

        // The caller's own removal is the one event a re-resolve cannot express. On a private space
        // the caller's GET now answers 403, and fetchSpace turns a denial into undefined without
        // dispatching, so the space and its pages would stay in the store and on screen. Evict
        // directly instead, which is what leaveSpace already does for a self-initiated leave.
        // Another member's removal changes the roster, so it re-reads that as well as the space:
        // fetchSpace only refreshes the space entity, and the member list is a separate slice that
        // nothing else refetches once the surface has mounted.
        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_MEMBER_REMOVED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (!spaceId) {
                return;
            }
            if (msg.data?.user_id === getCurrentUserId(store.getState())) {
                store.dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
                return;
            }
            store.dispatch(fetchSpace(spaceId) as never);
            store.dispatch(fetchSpaceMembers(spaceId) as never);
        });

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

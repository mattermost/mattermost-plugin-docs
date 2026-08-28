// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {publishPagePresence} from 'client/presence_events';
import type {PagePresenceEvent} from 'client/presence_events';
import manifest from 'manifest';
import type {Reducer, Store} from 'redux';
import {DOCS_BASE_URL, DOCS_SWITCHER_LINK_URL} from 'routing/paths';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {SpaceTypes} from 'store/action_types';
import {fetchSpace, fetchSpaceMembers, fetchSpaces, refreshSpaceAfterMemberPermissionsChanged, refreshSpaceAfterSelfRemoval} from 'store/actions';
import reducer from 'store/reducer';

import DocsRootLazy from 'components/docs_root/docs_root_lazy';
import DocsSettingsButton from 'components/docs_settings_button/docs_settings_button';

import type {PluginRegistry} from 'types/mattermost-webapp';
import type {DocsDispatch} from 'types/store';

// Compass glyph for the product-switcher icon. The host resolves this name
// through its glyph map and renders it at size 24 (and accent-colored in the
// switcher menu), the same path the built-in products use. There is no
// `product-docs` glyph yet, so a stock document glyph stands in.
const SWITCHER_ICON = 'file-text-outline';

const DocsHeaderCentre = () => null;

const PAGE_PRESENCE_EVENT = `custom_${manifest.id}_page_presence_updated`;

// This event also invalidates the hook-local grant matrix.
const SPACE_MEMBER_PERMISSIONS_EVENT = `custom_${manifest.id}_space_member_permissions_updated`;

// A team's space list has no other live signal that a space was added to it.
const SPACE_CREATED_EVENT = `custom_${manifest.id}_space_created`;

// This event changes the current user's resolved space access.
const SPACE_UPDATED_EVENT = `custom_${manifest.id}_space_updated`;

// A deleted space cannot be re-resolved.
const SPACE_DELETED_EVENT = `custom_${manifest.id}_space_deleted`;

// Reverses a prior SPACE_DELETED_EVENT eviction.
const SPACE_RESTORED_EVENT = `custom_${manifest.id}_space_restored`;

// Membership events also invalidate the roster slice.
const SPACE_MEMBER_ADDED_EVENT = `custom_${manifest.id}_space_member_added`;

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

        // The host's bare Store type only accepts plain actions; this plugin's thunks need the
        // store's own action-aware dispatch type.
        const dispatch = store.dispatch as unknown as DocsDispatch;

        registry.registerWebSocketEventHandler<PagePresenceEvent>(PAGE_PRESENCE_EVENT, (msg) => {
            publishPagePresence(msg.data);
        });

        // Refresh shared access before invalidating grants so a revoked manage tier is visible
        // before the settings hook decides whether it may reload the matrix.
        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_MEMBER_PERMISSIONS_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (spaceId) {
                dispatch(refreshSpaceAfterMemberPermissionsChanged(spaceId));
            }
        });

        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_CREATED_EVENT, () => {
            dispatch(fetchSpaces());
        });

        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_UPDATED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (spaceId) {
                dispatch(fetchSpace(spaceId));
            }
        });

        // One delete event invalidates the space and its page subtree.
        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_DELETED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (!spaceId) {
                return;
            }
            dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
        });

        // Re-fetches rather than just un-evicting: a restore can also change what the caller
        // may now do with the space, not just whether it exists.
        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_RESTORED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (spaceId) {
                dispatch(fetchSpace(spaceId));
            }
        });

        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_MEMBER_ADDED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (!spaceId) {
                return;
            }
            dispatch(fetchSpace(spaceId));
            dispatch(fetchSpaceMembers(spaceId));
        });

        // Self-removal may leave an open space readable; evict only after a definitive denial.
        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_MEMBER_REMOVED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (!spaceId) {
                return;
            }
            if (msg.data?.user_id === getCurrentUserId(store.getState())) {
                dispatch(refreshSpaceAfterSelfRemoval(spaceId));
                return;
            }
            dispatch(fetchSpace(spaceId));
            dispatch(fetchSpaceMembers(spaceId));
        });

        // Reconciles any of the above events missed while the connection was down.
        registry.registerReconnectHandler(() => {
            dispatch(fetchSpaces());
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

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {publishPagePresence} from 'client/presence_events';
import type {PagePresenceEvent} from 'client/presence_events';
import manifest from 'manifest';
import type {Reducer, Store} from 'redux';
import {DOCS_BASE_URL, DOCS_SWITCHER_LINK_URL} from 'routing/paths';

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {SpaceTypes} from 'store/action_types';
import {fetchSpace, fetchSpaceMembers, refreshSpaceAfterSelfRemoval, spaceMemberPermissionsChanged} from 'store/actions';
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
// member's effective permissions without any member row changing.
const SPACE_ACCESS_EVENTS = [
    `custom_${manifest.id}_space_member_permissions_updated`,
    `custom_${manifest.id}_space_updated`,
];

// The one access event that also moves the per-member grant matrix, which is not store state.
const SPACE_MEMBER_PERMISSIONS_EVENT = `custom_${manifest.id}_space_member_permissions_updated`;

// Not part of the re-resolve set: the space is gone, so there is nothing to re-read, and the
// server publishes this to a member snapshot taken before the backing channel is archived.
const SPACE_DELETED_EVENT = `custom_${manifest.id}_space_deleted`;

// Handled apart from the re-resolve set: an addition changes the roster, and the roster is a
// separate slice that nothing else refetches once the surface has mounted — the same reason the
// removal below re-reads it.
const SPACE_MEMBER_ADDED_EVENT = `custom_${manifest.id}_space_member_added`;

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
        // reads — the caller's own tier, the space's default set, its view access — so one
        // dispatch settles them all. The grant matrix is the exception: it is not store state, so
        // the event that moves it additionally bumps the revision its surface watches.
        for (const event of SPACE_ACCESS_EVENTS) {
            registry.registerWebSocketEventHandler<SpaceAccessEvent>(event, (msg) => {
                const spaceId = msg.data?.space_id;
                if (!spaceId) {
                    return;
                }
                store.dispatch(fetchSpace(spaceId) as never);
                if (event === SPACE_MEMBER_PERMISSIONS_EVENT) {
                    store.dispatch(spaceMemberPermissionsChanged(spaceId));
                }
            });
        }

        // A delete cascades to every page in the space but publishes only this one event, so the
        // client has to treat it as an invalidation of the whole tree — which is what the reducer
        // does with it. The deleting client has already pruned the same space; a repeat prunes
        // nothing further.
        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_DELETED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (!spaceId) {
                return;
            }
            store.dispatch({type: SpaceTypes.DELETED_SPACE, spaceId});
        });

        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_MEMBER_ADDED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (!spaceId) {
                return;
            }
            store.dispatch(fetchSpace(spaceId) as never);
            store.dispatch(fetchSpaceMembers(spaceId) as never);
        });

        // The caller's own removal goes through its own re-resolve. A plain fetchSpace cannot serve
        // it — that turns a denial into undefined without dispatching, leaving a private space the
        // caller can no longer read on screen — but neither can evicting on the event alone, since
        // an open space stays readable through the team fall-through and would vanish from a caller
        // who still has access to it. refreshSpaceAfterSelfRemoval keeps the two apart, evicting
        // only on the server's refusal. Another member's removal changes the roster, so it re-reads
        // that as well as the space, exactly as the addition above does.
        registry.registerWebSocketEventHandler<SpaceAccessEvent>(SPACE_MEMBER_REMOVED_EVENT, (msg) => {
            const spaceId = msg.data?.space_id;
            if (!spaceId) {
                return;
            }
            if (msg.data?.user_id === getCurrentUserId(store.getState())) {
                store.dispatch(refreshSpaceAfterSelfRemoval(spaceId) as never);
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

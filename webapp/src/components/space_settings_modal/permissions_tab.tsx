// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useCustomDefaultsAvailable} from 'hooks/permissions';
import {useSpaceAccessEditor} from 'hooks/space_access_editor';
import React, {useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {summarizePermissions} from 'utils/space_permission_sets';

import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import PublicPrivateSelector from 'components/form_controls/public_private_selector';
import {AddMembersField, MemberList} from 'components/space_members';

import type {Space} from 'types/docs';
import type {Permission} from 'types/permissions';
import {DEFAULT_PERMISSION_ORDER, MEMBER_PERMISSION_ORDER, Permissions} from 'types/permissions';

import DefaultPermissionTierSelector from './default_permission_tier_selector';
import PermissionToggles from './permission_toggles';
import {Section} from './space_settings_modal';
import styles from './space_settings_modal.module.scss';

// The server returns the complete authority in `permissions`, separately from the direct
// `granted_permissions` that the checkboxes edit. Keep the complete set in a stable, readable
// order so inherited/default authority is visible without making those permissions look like
// individual grants.
const EFFECTIVE_PERMISSION_ORDER: readonly Permission[] = [
    Permissions.READ_PAGE,
    ...MEMBER_PERMISSION_ORDER,
    Permissions.MANAGE_SPACE,
    Permissions.DELETE_SPACE,
];

/** Immediate-write access, default-permission, and membership settings. */
const PermissionsTab = ({space, onClose}: {space: Space; onClose: () => void}) => {
    const {formatMessage} = useIntl();
    const {
        permissions,
        members,
        memberIds,
        canEditAccess,
        grantOptionsFor,
        accessBusy,
        rosterBusy,
        loadFailedReason,
        busyReason,
        roleForMember,
        actions,
        addMembers,
    } = useSpaceAccessEditor(space, {onClose});
    const customDefaultsAvailable = useCustomDefaultsAvailable();

    const defaultTier = summarizePermissions(permissions.defaults);

    // Bind toggles to grants, not effective permissions, so per-member overrides stay independent
    // of the space defaults. The effective list is stated for every member; the toggles appear
    // only for the grants this caller may actually make on them.
    const renderMemberPermissions = (profile: MemberProfile) => {
        const record = permissions.members.get(profile.id);
        if (!record) {
            return null;
        }

        const grantOptions = grantOptionsFor(profile);

        // Held, but not through a grant this caller can edit: the read baseline, the space
        // default, the team-scoped permissions. Listed beside the checkboxes so the row states
        // the member's whole authority once, rather than a summary line that repeats the
        // vocabulary below it and appears to contradict it.
        const inherited = EFFECTIVE_PERMISSION_ORDER.filter((permission) => (
            record.permissions.includes(permission) && !grantOptions.includes(permission)
        ));

        // Where a permission the member holds comes from. A grantable one is annotated only when
        // the space default already provides it, since ticking it then adds a grant that changes
        // nothing today and survives the default being lowered later.
        const noteFor = (permission: Permission): string | undefined => {
            const fromDefault = permissions.defaults.includes(permission);
            if (grantOptions.includes(permission)) {
                if (record.granted_permissions.includes(permission)) {
                    return undefined;
                }
                if (fromDefault) {
                    return formatMessage({
                        id: 'docs.spaceSettings.permissions.alsoFromDefault',
                        defaultMessage: 'Also from the space default',
                    });
                }

                // Held without a grant and without the default: a space administrator holds the
                // whole page vocabulary through the scheme's admin role. Left unsaid, the row
                // reads as an unticked box beside a permission the member demonstrably has.
                if (record.permissions.includes(permission) && record.is_admin) {
                    return formatMessage({
                        id: 'docs.spaceSettings.permissions.alsoFromAdmin',
                        defaultMessage: 'Also from their administrator role',
                    });
                }
                return undefined;
            }
            if (permission === Permissions.READ_PAGE) {
                return formatMessage({
                    id: 'docs.spaceSettings.permissions.fromBaseline',
                    defaultMessage: 'Everyone with access',
                });
            }
            if (permission === Permissions.MANAGE_SPACE || permission === Permissions.DELETE_SPACE) {
                return formatMessage({
                    id: 'docs.spaceSettings.permissions.fromTeam',
                    defaultMessage: 'From their team role',
                });
            }
            if (permission === Permissions.ADMIN_SPACE) {
                return formatMessage({
                    id: 'docs.spaceSettings.permissions.fromAdmin',
                    defaultMessage: 'Space administrator',
                });
            }
            return fromDefault ? formatMessage({
                id: 'docs.spaceSettings.permissions.fromDefault',
                defaultMessage: 'From the space default',
            }) : undefined;
        };

        return (
            <div className={styles.memberPermissions}>
                {record.is_auto_joined && (

                    // Surface the provenance marker recorded by the server; its cleanup is best-effort.
                    <span className={styles.autoJoinedNote}>
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.autoJoined'
                            defaultMessage='Joined automatically by editing this space'
                        />
                    </span>
                )}
                <PermissionToggles
                    options={grantOptions}
                    staticOptions={inherited}
                    noteFor={noteFor}
                    selected={record.granted_permissions}
                    disabled={rosterBusy}
                    disabledReason={busyReason}
                    busy={permissions.busy}
                    idPrefix={`member-${profile.id}`}
                    legend={formatMessage({
                        id: 'docs.spaceSettings.permissions.memberLegend',
                        defaultMessage: 'Permissions for {username}',
                    }, {username: profile.username})}
                    hideLegend={true}
                    onChange={(next) => {
                        permissions.setMemberGrants(profile.id, next).catch(() => {});
                    }}
                />
            </div>
        );
    };

    // Values use the server's view_access vocabulary; "Public" remains the user-facing label.
    const accessOptions = useMemo(() => [
        {
            value: 'open',
            icon: <GlobeIcon size={20}/>,
            title: formatMessage({id: 'docs.spaceSettings.permissions.public.title', defaultMessage: 'Public'}),
            description: formatMessage({id: 'docs.spaceSettings.permissions.public.description', defaultMessage: 'Anyone in the team can find and view this space.'}),
            disabled: accessBusy,
            disabledReason: busyReason,
        },
        {
            value: 'private',
            icon: <LockOutlineIcon size={20}/>,
            title: formatMessage({id: 'docs.spaceSettings.permissions.private.title', defaultMessage: 'Private'}),

            // Self-join markers select removals; deliberate membership changes normally clear them.
            description: formatMessage({id: 'docs.spaceSettings.permissions.private.description', defaultMessage: 'Only invited members can view. People who joined by authoring while public lose access.'}),
            disabled: accessBusy,
            disabledReason: busyReason,
        },
    ], [formatMessage, accessBusy, busyReason]);

    // The named tiers come first on every surface; a licensed install may refine the set below
    // them, permission by permission.
    const defaultTiers = (
        <DefaultPermissionTierSelector
            spaceId={space.id}
            selected={permissions.defaults}
            disabled={accessBusy}
            customDefaultsAvailable={customDefaultsAvailable}
            onChange={(next) => {
                permissions.setDefaults(next).catch(() => {});
            }}
        />
    );

    // Both exposure sections are a space administrator's to set. A caller without that authority
    // is offered neither control: the server would refuse the write, and their own authority is
    // still legible from the effective list on their row below.
    return (
        <>
            {loadFailedReason && <p className={styles.helper}>{loadFailedReason}</p>}
            {canEditAccess && (
                <Section
                    title={(
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.accessHeading'
                            defaultMessage='Space access'
                        />
                    )}
                >
                    <PublicPrivateSelector
                        ariaLabel={formatMessage({id: 'docs.spaceSettings.permissions.accessLabel', defaultMessage: 'Space access'})}
                        options={accessOptions}
                        value={permissions.viewAccess}
                        onChange={(value) => {
                        // The hook reports the rejection; consume it here to avoid an unhandled promise.
                            permissions.setViewAccess(value as typeof permissions.viewAccess).catch(() => {});
                        }}
                    />
                </Section>
            )}

            {canEditAccess && (
                <Section
                    title={(
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.permissionsHeading'
                            defaultMessage='What members can do'
                        />
                    )}
                >
                    <PermissionToggles
                        options={customDefaultsAvailable ? DEFAULT_PERMISSION_ORDER : []}
                        selected={permissions.defaults}
                        disabled={accessBusy}
                        disabledReason={busyReason}
                        busy={permissions.busy}
                        idPrefix='space-default'
                        legend={(
                            <FormattedMessage
                                id='docs.spaceSettings.permissions.permissionsLegend'
                                defaultMessage='Everyone with access to this space can:'
                            />
                        )}
                        header={defaultTiers}
                        onChange={(next) => {
                            permissions.setDefaults(next).catch(() => {});
                        }}
                    />
                    {!customDefaultsAvailable && (
                        <p className={styles.helper}>
                            {defaultTier === 'custom' ? (
                                <FormattedMessage
                                    id='docs.spaceSettings.permissions.preset.customCurrent'
                                    defaultMessage='This space currently uses a custom permission combination. Choose a tier to replace it; custom combinations require a Professional or Enterprise license that includes guest account permissions.'
                                />
                            ) : (
                                <FormattedMessage
                                    id='docs.spaceSettings.permissions.preset.licenseRequired'
                                    defaultMessage='Custom permission combinations require a Professional or Enterprise license that includes guest account permissions.'
                                />
                            )}
                        </p>
                    )}
                </Section>
            )}

            <Section
                title={(
                    <FormattedMessage
                        id='docs.spaceSettings.permissions.peopleHeading'
                        defaultMessage='People with access'
                    />
                )}
            >
                {permissions.canManageMembers && (
                    <AddMembersField
                        excludeIds={memberIds}
                        onAdd={addMembers}
                        disabled={rosterBusy}
                    />
                )}
                <MemberList
                    members={members}
                    avatarSize='sm'
                    spaceTitle={space.title}
                    actions={actions}
                    roleForMember={roleForMember}
                    renderBelowMember={renderMemberPermissions}
                />
            </Section>
        </>
    );
};

export default PermissionsTab;

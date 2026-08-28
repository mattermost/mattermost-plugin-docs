// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {usePermissionLabels} from 'hooks/permission_labels';
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
    const permissionLabels = usePermissionLabels();
    const {
        permissions,
        members,
        memberIds,
        adminLocked,
        rosterLocked,
        adminLockedReason,
        adminSpaceLockedReason,
        roleForMember,
        memberLockedReason,
        isMemberLocked,
        actions,
        addMembers,
    } = useSpaceAccessEditor(space, {onClose});
    const customDefaultsAvailable = useCustomDefaultsAvailable();

    const defaultTier = summarizePermissions(permissions.defaults);

    // Bind toggles to grants, not effective permissions, so per-member overrides stay independent
    // of the space defaults. Guest grants are invalid and remain visible but locked.
    const renderMemberPermissions = (profile: MemberProfile) => {
        const record = permissions.members.get(profile.id);
        if (!record) {
            return null;
        }

        const lockedReason = memberLockedReason(profile);

        const effectivePermissions = EFFECTIVE_PERMISSION_ORDER.
            filter((permission) => record.permissions.includes(permission)).
            map((permission) => permissionLabels[permission]);

        return (
            <div
                className={styles.memberPermissions}
                title={lockedReason}
            >
                {record.is_auto_joined && (

                    // Surface the provenance marker recorded by the server; its cleanup is best-effort.
                    <span className={styles.autoJoinedNote}>
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.autoJoined'
                            defaultMessage='Joined automatically by editing this space'
                        />
                    </span>
                )}
                <div
                    id={`member-${profile.id}-effective-permissions`}
                    className={styles.effectivePermissions}
                    role='group'
                    aria-label={formatMessage({
                        id: 'docs.spaceSettings.permissions.memberEffectiveLabel',
                        defaultMessage: 'Effective permissions for {username}',
                    }, {username: profile.username})}
                >
                    <span className={styles.effectivePermissionsLabel}>
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.memberEffective'
                            defaultMessage='Effective permissions:'
                        />
                    </span>
                    <span>{effectivePermissions.join(', ')}</span>
                </div>
                <PermissionToggles
                    options={MEMBER_PERMISSION_ORDER}
                    selected={record.granted_permissions}
                    disabled={isMemberLocked(profile)}
                    disabledReason={lockedReason}
                    disabledOptions={permissions.canAdminister ? undefined : [Permissions.ADMIN_SPACE]}
                    disabledOptionsReason={adminSpaceLockedReason}
                    busy={permissions.busy}
                    idPrefix={`member-${profile.id}`}
                    legend={(
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.memberLegend'
                            defaultMessage='In addition to what everyone can do:'
                        />
                    )}
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
            disabled: adminLocked,
            disabledReason: adminLockedReason,
        },
        {
            value: 'private',
            icon: <LockOutlineIcon size={20}/>,
            title: formatMessage({id: 'docs.spaceSettings.permissions.private.title', defaultMessage: 'Private'}),

            // Self-join markers select removals; deliberate membership changes normally clear them.
            description: formatMessage({id: 'docs.spaceSettings.permissions.private.description', defaultMessage: 'Only invited members can view. People who joined by authoring while public lose access.'}),
            disabled: adminLocked,
            disabledReason: adminLockedReason,
        },
    ], [formatMessage, adminLocked, adminLockedReason]);

    // The named tiers come first on every surface; a licensed install may refine the set below
    // them, permission by permission.
    const defaultTiers = (
        <DefaultPermissionTierSelector
            spaceId={space.id}
            selected={permissions.defaults}
            disabled={adminLocked || permissions.busy}
            customDefaultsAvailable={customDefaultsAvailable}
            onChange={(next) => {
                permissions.setDefaults(next).catch(() => {});
            }}
        />
    );

    return (
        <>
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
                    disabled={adminLocked || permissions.busy}
                    disabledReason={adminLockedReason}
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

            <Section
                title={(
                    <FormattedMessage
                        id='docs.spaceSettings.permissions.peopleHeading'
                        defaultMessage='People with access'
                    />
                )}
            >
                <AddMembersField
                    excludeIds={memberIds}
                    onAdd={addMembers}
                    disabled={rosterLocked}
                />
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

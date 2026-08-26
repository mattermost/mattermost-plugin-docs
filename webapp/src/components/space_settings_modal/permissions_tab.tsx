// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import {useSpaceMemberProfiles} from 'hooks/members';
import {useAppSelector} from 'hooks/redux';
import {useManageSpaceMembers} from 'hooks/space_members';
import {useSpacePermissions} from 'hooks/space_permissions';
import React, {useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import {getLicense} from 'mattermost-redux/selectors/entities/general';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import PublicPrivateSelector from 'components/form_controls/public_private_selector';
import {AddMembersField, MemberList} from 'components/space_members';
import type {MemberListActions} from 'components/space_members';

import type {Space} from 'types/docs';
import type {Permission} from 'types/permissions';
import {DEFAULT_PERMISSION_ORDER, MEMBER_PERMISSION_ORDER, Permissions} from 'types/permissions';

import PermissionToggles from './permission_toggles';
import {Section} from './space_settings_modal';
import styles from './space_settings_modal.module.scss';

type DefaultPermissionPreset = {
    id: 'contribute' | 'comment' | 'read_only';
    permissions: readonly Permission[];
};

// Core seeds these three schemes and resolves them without creating a licensed custom scheme.
// Every other default-permission set goes through the custom-scheme pool.
const DEFAULT_PERMISSION_PRESETS: readonly DefaultPermissionPreset[] = [
    {
        id: 'contribute',
        permissions: [Permissions.CREATE_PAGE, Permissions.COMMENT_PAGE, Permissions.EDIT_PAGE, Permissions.DELETE_OWN_PAGE],
    },
    {
        id: 'comment',
        permissions: [Permissions.COMMENT_PAGE],
    },
    {
        id: 'read_only',
        permissions: [],
    },
];

const samePermissionSet = (left: readonly Permission[], right: readonly Permission[]) =>
    left.length === right.length && left.every((permission) => right.includes(permission));

/** Immediate-write access, default-permission, and membership settings. */
const PermissionsTab = ({space, onClose}: {space: Space; onClose: () => void}) => {
    const {formatMessage} = useIntl();
    const currentUserId = useAppSelector(getCurrentUserId);
    const license = useAppSelector(getLicense);
    const members = useSpaceMemberProfiles(space.id);
    const {addMembers, removeMember, leave} = useManageSpaceMembers(space);
    const permissions = useSpacePermissions(space);
    const customDefaultsAvailable = license.CustomPermissionsSchemes === 'true' || license.SkuShortName === 'professional';

    // Exposure and roster writes have different authority tiers. Disable both while the hook
    // reports a write in flight to avoid overlapping reconciliation from this surface.
    const adminLocked = !permissions.canAdminister || permissions.loading || permissions.busy;
    const rosterLocked = !permissions.canManageMembers || permissions.loading || permissions.busy;

    const memberIds = useMemo(() => members.map((member) => member.id), [members]);
    const defaultPermissionPresets = useMemo(() => [
        {
            ...DEFAULT_PERMISSION_PRESETS[0],
            label: formatMessage({id: 'docs.spaceSettings.permissions.preset.contribute', defaultMessage: 'Contribute'}),
            description: formatMessage({id: 'docs.spaceSettings.permissions.preset.contributeDescription', defaultMessage: 'Create, comment on, edit, and delete their own pages'}),
        },
        {
            ...DEFAULT_PERMISSION_PRESETS[1],
            label: formatMessage({id: 'docs.spaceSettings.permissions.preset.comment', defaultMessage: 'Comment'}),
            description: formatMessage({id: 'docs.spaceSettings.permissions.preset.commentDescription', defaultMessage: 'View and comment on pages'}),
        },
        {
            ...DEFAULT_PERMISSION_PRESETS[2],
            label: formatMessage({id: 'docs.spaceSettings.permissions.preset.readOnly', defaultMessage: 'Read only'}),
            description: formatMessage({id: 'docs.spaceSettings.permissions.preset.readOnlyDescription', defaultMessage: 'View pages'}),
        },
    ], [formatMessage]);
    const selectedDefaultPreset = defaultPermissionPresets.find((preset) => samePermissionSet(preset.permissions, permissions.defaults));

    // Bind toggles to grants, not effective permissions, or defaults would become pinned as
    // per-member overrides. Guest grants are invalid and remain visible but locked.
    const renderMemberPermissions = (profile: MemberProfile) => {
        const record = permissions.members.get(profile.id);
        if (!record) {
            return null;
        }

        let lockedReason;
        if (record.is_guest) {
            lockedReason = formatMessage({
                id: 'docs.spaceSettings.permissions.guestLocked',
                defaultMessage: 'Guests can only view pages',
            });
        } else if (profile.id === currentUserId && !permissions.canAdminister) {
            lockedReason = formatMessage({
                id: 'docs.spaceSettings.permissions.selfLocked',
                defaultMessage: 'Only a space administrator can change their own permissions',
            });
        }

        return (
            <div
                className={styles.memberPermissions}
                title={lockedReason}
            >
                {record.auto_joined && (

                    // Surface the provenance marker recorded by the server; its cleanup is best-effort.
                    <span className={styles.autoJoinedNote}>
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.autoJoined'
                            defaultMessage='Joined automatically by editing this space'
                        />
                    </span>
                )}
                <PermissionToggles
                    options={MEMBER_PERMISSION_ORDER}
                    selected={record.granted_permissions}
                    disabled={rosterLocked || permissions.busy || record.is_guest || (profile.id === currentUserId && !permissions.canAdminister)}
                    disabledOptions={permissions.canAdminister ? undefined : [Permissions.ADMIN_SPACE]}
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

    // Close settings after a successful self-removal.
    const actions: MemberListActions = {
        onRemove: removeMember,
        onLeave: async () => {
            if (await leave()) {
                onClose();
            }
        },
        disabled: rosterLocked,
    };

    // Values use the server's view_access vocabulary; "Public" remains the user-facing label.
    const accessOptions = useMemo(() => {
        // Keep descriptors literal for message extraction.
        const adminOnly = formatMessage({
            id: 'docs.spaceSettings.permissions.adminOnly',
            defaultMessage: 'Only a space administrator can change this',
        });
        const readFailed = formatMessage({
            id: 'docs.spaceSettings.permissions.loadFailed',
            defaultMessage: "Couldn't load this space's permissions. Close and reopen settings to try again.",
        });

        // Only a resolved authority denial uses the admin-only explanation.
        let lockedReason;
        if (permissions.loadFailed) {
            lockedReason = readFailed;
        } else if (!permissions.loading && !permissions.busy) {
            lockedReason = adminOnly;
        }

        return [
            {
                value: 'open',
                icon: <GlobeIcon size={20}/>,
                title: formatMessage({id: 'docs.spaceSettings.permissions.public.title', defaultMessage: 'Public'}),
                description: formatMessage({id: 'docs.spaceSettings.permissions.public.description', defaultMessage: 'Anyone in the team can find and view this space.'}),
                disabled: adminLocked,
                disabledReason: lockedReason,
            },
            {
                value: 'private',
                icon: <LockOutlineIcon size={20}/>,
                title: formatMessage({id: 'docs.spaceSettings.permissions.private.title', defaultMessage: 'Private'}),

                // Self-join markers select removals; deliberate membership changes normally clear them.
                description: formatMessage({id: 'docs.spaceSettings.permissions.private.description', defaultMessage: 'Only invited members can view. People who joined by authoring while public lose access.'}),
                disabled: adminLocked,
                disabledReason: lockedReason,
            },
        ];
    }, [formatMessage, adminLocked, permissions.loading, permissions.loadFailed, permissions.busy]);

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
                {customDefaultsAvailable ? (
                    <PermissionToggles
                        options={DEFAULT_PERMISSION_ORDER}
                        selected={permissions.defaults}
                        disabled={adminLocked || permissions.busy}
                        busy={permissions.busy}
                        idPrefix='space-default'
                        legend={(
                            <FormattedMessage
                                id='docs.spaceSettings.permissions.permissionsLegend'
                                defaultMessage='Everyone with access to this space can:'
                            />
                        )}
                        onChange={(next) => {
                            permissions.setDefaults(next).catch(() => {});
                        }}
                    />
                ) : (
                    <>
                        <fieldset
                            className={styles.presets}
                            aria-busy={permissions.busy}
                        >
                            <legend className={styles.togglesLegend}>
                                <FormattedMessage
                                    id='docs.spaceSettings.permissions.permissionsLegend'
                                    defaultMessage='Everyone with access to this space can:'
                                />
                            </legend>
                            {defaultPermissionPresets.map((preset) => {
                                const id = `space-default-preset-${preset.id}`;
                                return (
                                    <div
                                        key={preset.id}
                                        className={styles.preset}
                                    >
                                        <input
                                            id={id}
                                            type='radio'
                                            name={`space-default-preset-${space.id}`}
                                            checked={selectedDefaultPreset?.id === preset.id}
                                            disabled={adminLocked || permissions.busy}
                                            aria-label={preset.label}
                                            aria-describedby={`${id}-description`}
                                            onChange={() => {
                                                permissions.setDefaults([...preset.permissions]).catch(() => {});
                                            }}
                                        />
                                        <label htmlFor={id}>
                                            <span className={styles.presetTitle}>{preset.label}</span>
                                            <span
                                                id={`${id}-description`}
                                                className={styles.presetDescription}
                                            >
                                                {preset.description}
                                            </span>
                                        </label>
                                    </div>
                                );
                            })}
                        </fieldset>
                        <p className={styles.helper}>
                            {selectedDefaultPreset ? (
                                <FormattedMessage
                                    id='docs.spaceSettings.permissions.preset.licenseRequired'
                                    defaultMessage='Custom permission combinations require a Professional or Enterprise license.'
                                />
                            ) : (
                                <FormattedMessage
                                    id='docs.spaceSettings.permissions.preset.customCurrent'
                                    defaultMessage='This space currently uses a custom permission combination. Choose a preset to replace it; custom combinations require a Professional or Enterprise license.'
                                />
                            )}
                        </p>
                    </>
                )}
            </Section>

            <Section
                title={(
                    <FormattedMessage
                        id='docs.spaceSettings.permissions.peopleHeading'
                        defaultMessage='People and groups with access'
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
                    roleForMember={(profile) => {
                        const record = permissions.members.get(profile.id);
                        if (!record) {
                            return undefined;
                        }
                        if (record.is_guest) {
                            return 'guest';
                        }
                        return record.is_admin ? 'admin' : 'member';
                    }}
                    renderBelowMember={renderMemberPermissions}
                />
            </Section>

            <section className={styles.section}>
                <div className={styles.toggleRow}>
                    <span className={styles.toggleText}>
                        <span className={styles.toggleTitle}>
                            <FormattedMessage
                                id='docs.spaceSettings.permissions.externalSharing.title'
                                defaultMessage='External sharing'
                            />
                        </span>
                        <span className={styles.helper}>
                            <FormattedMessage
                                id='docs.spaceSettings.permissions.externalSharing.description'
                                defaultMessage='Let people outside the team access this space with a link.'
                            />
                        </span>
                    </span>
                    <span className={styles.comingSoonPill}>
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.externalSharing.comingSoon'
                            defaultMessage='Coming soon'
                        />
                    </span>
                </div>
            </section>
        </>
    );
};

export default PermissionsTab;

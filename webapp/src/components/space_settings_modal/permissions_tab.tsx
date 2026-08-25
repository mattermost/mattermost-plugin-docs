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

import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import PublicPrivateSelector from 'components/form_controls/public_private_selector';
import {AddMembersField, MemberList} from 'components/space_members';
import type {MemberListActions} from 'components/space_members';

import type {Space} from 'types/docs';
import {DEFAULT_PERMISSION_ORDER, MEMBER_PERMISSION_ORDER, Permissions} from 'types/permissions';

import PermissionToggles from './permission_toggles';
import {Section} from './space_settings_modal';
import styles from './space_settings_modal.module.scss';

/**
 * Space Settings → Permissions.
 *
 * The people section is the shared member core; around it sit the space's own two
 * exposure controls — who can find the space (view access) and what its members can do
 * (the default permission set). Both are admin-only and both apply immediately, as
 * membership changes do: each is already committed when its request returns, so
 * SaveChangesBar would imply a discard that cannot happen.
 *
 * External sharing remains scaffolding; there is no server surface for it yet.
 */
const PermissionsTab = ({space, onClose}: {space: Space; onClose: () => void}) => {
    const {formatMessage} = useIntl();
    const currentUserId = useAppSelector(getCurrentUserId);
    const members = useSpaceMemberProfiles(space.id);
    const {addMembers, removeMember, leave} = useManageSpaceMembers(space);
    const permissions = useSpacePermissions(space);

    // A non-admin, and anyone whose permission state has not loaded, sees the controls
    // but cannot move them: rendering them read-only says what the space is configured
    // to allow, which is worth showing even when you may not change it.
    //
    // busy is included so a save in flight locks the access control too, as it already does
    // the permission toggles: both mutations send the same optimistic-lock baseline, so a
    // second click before the first returns would race on a stale update_at.
    // Two locks, because the server gates these controls at two different tiers. The space-wide
    // knobs (view_access, the default permission set) admit a sysadmin or a space admin;
    // the member roster additionally admits a team manage_space holder. Locking the roster on
    // canAdminister would hide it from a team admin the roster routes would have served.
    const adminLocked = !permissions.canAdminister || permissions.loading || permissions.busy;
    const rosterLocked = !permissions.canManageMembers || permissions.loading || permissions.busy;

    const memberIds = useMemo(() => members.map((member) => member.id), [members]);

    /**
     * The per-member half of the permission matrix: a row of toggles under each person,
     * carrying what they hold *in addition* to the space default.
     *
     * Bound to granted_permissions, not the effective set, because that is what the write
     * endpoint replaces — binding to effective would resend the space default as a
     * per-member grant and pin it against a later default change.
     *
     * A guest's row is rendered locked rather than hidden: the server refuses to grant a
     * guest anything (app.space.member.guest_not_assignable), and showing why is more
     * useful than a row that silently offers nothing.
     */
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

                    // Surfaced because privatizing a space does not remove members: an admin
                    // pruning access needs to tell the people who let themselves in by writing to
                    // the space while it was public from the ones who were deliberately added.
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

    // Leaving destroys your access to what is behind this tab, so the settings
    // modal goes too (mirrors ShareSpaceModal's onLeave).
    const actions: MemberListActions = {
        onRemove: removeMember,
        onLeave: async () => {
            if (await leave()) {
                onClose();
            }
        },
        disabled: rosterLocked,
    };

    // The option values are the server's own view_access vocabulary ('open'/'private')
    // rather than the create-space flow's 'public', so no mapping sits between this
    // control and the request it makes. The label stays "Public", which is what the
    // setting means to a reader.
    const accessOptions = useMemo(() => {
        // Separate calls rather than one formatMessage over a conditional descriptor:
        // extraction reads the literal argument, so a conditional would produce no key.
        const adminOnly = formatMessage({
            id: 'docs.spaceSettings.permissions.adminOnly',
            defaultMessage: 'Only a space administrator can change this',
        });
        const readFailed = formatMessage({
            id: 'docs.spaceSettings.permissions.loadFailed',
            defaultMessage: "Couldn't load this space's permissions. Close and reopen settings to try again.",
        });

        // A read still in flight has nothing to explain, and a read that failed is not an
        // answer about the caller's own role — only a resolved non-admin is told they are one.
        // A save in flight is the same case as a read: adminLocked is true for the duration, but
        // the lock is transient and says nothing about the caller's role, so telling an admin
        // mid-save that they are not one would be false.
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

                // An invitation is an individual grant and survives. A membership created only so
                // somebody could author under the open-team grant is pruned with that grant.
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
                        // Rejections are already reported by the hook; nothing here can add to
                        // them, and an unhandled rejection would surface as a console error.
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

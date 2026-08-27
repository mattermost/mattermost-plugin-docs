// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceMemberProfiles} from 'hooks/members';
import {useDocsNavigation} from 'hooks/navigation';
import {usePermissionLabels} from 'hooks/permission_labels';
import {useAppSelector} from 'hooks/redux';
import {useManageSpaceMembers} from 'hooks/space_members';
import {useSpacePermissions} from 'hooks/space_permissions';
import React, {useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';
import {DEFAULT_PERMISSION_PRESETS, samePermissionSet, summarizePermissions} from 'utils/space_permission_sets';

import CheckIcon from '@mattermost/compass-icons/components/check';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import ContentCopyIcon from '@mattermost/compass-icons/components/content-copy';
import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import {getLicense} from 'mattermost-redux/selectors/entities/general';
import {getCurrentUserId} from 'mattermost-redux/selectors/entities/users';

import {Button, SecondaryButton} from 'components/form_controls/button';
import GenericModal from 'components/generic_modal/generic_modal';
import Menu from 'components/menu/menu';
import {AddMembersField, MemberList} from 'components/space_members';
import type {MemberListActions} from 'components/space_members';

import type {Space} from 'types/docs';
import {DEFAULT_PERMISSION_ORDER, MEMBER_PERMISSION_ORDER, Permissions, type Permission} from 'types/permissions';

import styles from './share_space_modal.module.scss';

type Props = {
    space: Space;
    onClose: () => void;
};

// Primary space-sharing surface. The compact controls preserve the approved Share modal while
// their menus expose the complete capability set implemented by the permission service.
const ShareSpaceModal = ({space, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const permissionLabels = usePermissionLabels();
    const {paths: absolutePaths} = useDocsNavigation({absolute: true});
    const currentUserId = useAppSelector(getCurrentUserId);
    const license = useAppSelector(getLicense);
    const members = useSpaceMemberProfiles(space.id);
    const {addMembers, removeMember, leave, busy} = useManageSpaceMembers(space);
    const permissions = useSpacePermissions(space);

    const customDefaultsAvailable = license.CustomPermissionsSchemes === 'true' || license.SkuShortName === 'professional';
    const adminLocked = !permissions.canAdminister || permissions.loading || permissions.busy;
    const rosterLocked = !permissions.canManageMembers || permissions.loading || permissions.busy || busy;

    const memberIds = useMemo(() => members.map((member) => member.id), [members]);

    const actions: MemberListActions = {
        ...(permissions.canManageMembers && {onRemove: removeMember}),
        onLeave: async () => {
            if (await leave()) {
                onClose();
            }
        },
        disabled: permissions.busy || busy,
    };

    const copyLink = () => copyToClipboard(absolutePaths.space(space.id));

    const title = (
        <FormattedMessage
            id='docs.share.title'
            defaultMessage="Share ''{name}''"
            values={{name: space.title}}
        />
    );

    const titleActions = (
        <SecondaryButton
            size='sm'
            className={styles.copyLink}
            onClick={copyLink}
        >
            <ContentCopyIcon size={16}/>
            <FormattedMessage
                id='docs.share.copyLink'
                defaultMessage='Copy link'
            />
        </SecondaryButton>
    );

    const defaultPresets = useMemo(() => [
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

    const defaultSummary = summarizePermissions(permissions.defaults);
    const defaultSummaryLabel = {
        view: formatMessage({id: 'docs.share.access.canView', defaultMessage: 'Can view'}),
        comment: formatMessage({id: 'docs.share.access.canComment', defaultMessage: 'Can comment'}),
        edit: formatMessage({id: 'docs.share.access.canEdit', defaultMessage: 'Can edit'}),
        custom: formatMessage({id: 'docs.share.access.custom', defaultMessage: 'Custom'}),
    }[defaultSummary];

    const isOpen = permissions.viewAccess === 'open';
    const readFailure = formatMessage({
        id: 'docs.share.permissions.loadFailed',
        defaultMessage: "Couldn't load this space's permissions. Close and reopen Share to try again.",
    });
    const adminOnly = formatMessage({
        id: 'docs.spaceSettings.permissions.adminOnly',
        defaultMessage: 'Only a space administrator can change this',
    });
    const lockedReason = permissions.loadFailed ? readFailure : adminOnly;

    const visibilityTrigger = (
        <button
            type='button'
            className={styles.accessTrigger}
            disabled={adminLocked}
            title={adminLocked ? lockedReason : undefined}
        >
            {isOpen ? <GlobeIcon size={16}/> : <LockOutlineIcon size={16}/>}
            {isOpen ? (
                <FormattedMessage
                    id='docs.share.visibility.public'
                    defaultMessage='Public'
                />
            ) : (
                <FormattedMessage
                    id='docs.share.visibility.private'
                    defaultMessage='Private'
                />
            )}
            <ChevronDownIcon size={16}/>
        </button>
    );

    const defaultPermissionsTrigger = (
        <Button
            type='button'
            emphasis='quaternary'
            size='sm'
            className={styles.canView}
            disabled={adminLocked}
            title={adminLocked ? lockedReason : undefined}
        >
            {defaultSummaryLabel}
            <ChevronDownIcon size={16}/>
        </Button>
    );

    const footer = (
        <div className={styles.access}>
            <div className={styles.accessLeft}>
                <Menu
                    ariaLabel={formatMessage({id: 'docs.share.visibility.menuLabel', defaultMessage: 'Space visibility'})}
                    trigger={visibilityTrigger}
                >
                    <Menu.Item
                        leadingIcon={<GlobeIcon size={16}/>}
                        trailingIcon={isOpen ? <CheckIcon size={16}/> : undefined}
                        onClick={() => permissions.setViewAccess('open').catch(() => {})}
                    >
                        <FormattedMessage
                            id='docs.share.visibility.public'
                            defaultMessage='Public'
                        />
                    </Menu.Item>
                    <Menu.Item
                        leadingIcon={<LockOutlineIcon size={16}/>}
                        trailingIcon={isOpen ? undefined : <CheckIcon size={16}/>}
                        onClick={() => permissions.setViewAccess('private').catch(() => {})}
                    >
                        <FormattedMessage
                            id='docs.share.visibility.private'
                            defaultMessage='Private'
                        />
                    </Menu.Item>
                </Menu>
                <span className={styles.accessHint}>
                    {isOpen ? (
                        <FormattedMessage
                            id='docs.share.visibility.publicHint'
                            defaultMessage='Any team member can view'
                        />
                    ) : (
                        <FormattedMessage
                            id='docs.share.visibility.privateHint'
                            defaultMessage='Only invited members'
                        />
                    )}
                </span>
            </div>
            <Menu
                ariaLabel={formatMessage({id: 'docs.share.access.menuLabel', defaultMessage: 'Default permissions for everyone with access'})}
                align='right'
                trigger={defaultPermissionsTrigger}
            >
                {customDefaultsAvailable ? DEFAULT_PERMISSION_ORDER.map((permission) => (
                    <Menu.CheckboxItem
                        key={permission}
                        checked={permissions.defaults.includes(permission)}
                        disabled={adminLocked}
                        onCheckedChange={(checked) => {
                            if (adminLocked) {
                                return;
                            }

                            const next = DEFAULT_PERMISSION_ORDER.filter((option) => (
                                option === permission ? checked : permissions.defaults.includes(option)
                            ));
                            permissions.setDefaults(next).catch(() => {});
                        }}
                    >
                        {permissionLabels[permission]}
                    </Menu.CheckboxItem>
                )) : defaultPresets.map((preset) => (
                    <Menu.Item
                        key={preset.id}
                        secondaryLabel={preset.description}
                        trailingIcon={samePermissionSet(preset.permissions, permissions.defaults) ? <CheckIcon size={16}/> : undefined}
                        onClick={() => permissions.setDefaults([...preset.permissions]).catch(() => {})}
                    >
                        {preset.label}
                    </Menu.Item>
                ))}
            </Menu>
        </div>
    );

    return (
        <GenericModal
            className={styles.modal}
            title={title}
            titleActions={titleActions}
            ariaLabel={formatMessage({id: 'docs.share.title', defaultMessage: "Share ''{name}''"}, {name: space.title})}
            onClose={onClose}
            footer={footer}
            footerClassName={styles.footer}
            headerDivider={false}
            footerDivider={true}
        >
            <div className={styles.body}>
                {permissions.canManageMembers && (
                    <div className={styles.search}>
                        <AddMembersField
                            excludeIds={memberIds}
                            onAdd={addMembers}
                            disabled={rosterLocked}
                            large={true}
                            commitOnSelect={true}
                        />
                    </div>
                )}
                <div className={styles.members}>
                    <MemberList
                        members={members}
                        avatarSize='md'
                        showYouBadge={true}
                        spaceTitle={space.title}
                        comfortable={true}
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
                        permissionMenuForMember={(profile) => {
                            const record = permissions.members.get(profile.id);
                            if (!record) {
                                return undefined;
                            }

                            return {
                                options: MEMBER_PERMISSION_ORDER,
                                selected: record.granted_permissions,
                                effective: record.permissions,
                                disabled: rosterLocked || record.is_guest || (profile.id === currentUserId && !permissions.canAdminister),
                                disabledOptions: permissions.canAdminister ? undefined : [Permissions.ADMIN_SPACE],
                                onChange: (next: Permission[]) => {
                                    permissions.setMemberGrants(profile.id, next).catch(() => {});
                                },
                            };
                        }}
                    />
                </div>
            </div>
        </GenericModal>
    );
};

export default ShareSpaceModal;

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceMemberProfiles} from 'hooks/members';
import {useManageSpaceMembers} from 'hooks/space_members';
import React, {useMemo} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import PublicPrivateSelector from 'components/form_controls/public_private_selector';
import {AddMembersField, MemberList} from 'components/space_members';
import type {MemberListActions} from 'components/space_members';

import type {Space} from 'types/docs';

import {Section} from './space_settings_modal';
import styles from './space_settings_modal.module.scss';

/**
 * Space Settings → Permissions.
 *
 * The people section is the shared member core; the access selector and the
 * external-sharing toggle around it stay scaffolding for PR #10. Membership changes
 * apply immediately and deliberately never mark the modal dirty — an add is already
 * committed when it returns, so SaveChangesBar would imply a discard that cannot
 * happen.
 */
const PermissionsTab = ({space, onClose}: {space: Space; onClose: () => void}) => {
    const {formatMessage} = useIntl();
    const members = useSpaceMemberProfiles(space.id);
    const {addMembers, removeMember, leave, busy} = useManageSpaceMembers(space);

    const memberIds = useMemo(() => members.map((member) => member.id), [members]);

    // Leaving destroys your access to what is behind this tab, so the settings
    // modal goes too (mirrors ShareSpaceModal's onLeave).
    const actions: MemberListActions = {
        onRemove: removeMember,
        onLeave: () => leave().then(onClose),
        disabled: busy,
    };

    // Scaffolding: view-access and per-member capabilities land with PR #10, so
    // the selector, search and role dropdowns are visual only.
    const accessOptions = useMemo(() => [
        {
            value: 'public',
            icon: <GlobeIcon size={20}/>,
            title: formatMessage({id: 'docs.spaceSettings.permissions.public.title', defaultMessage: 'Public'}),
            description: formatMessage({id: 'docs.spaceSettings.permissions.public.description', defaultMessage: 'Anyone in the team can find and view this space.'}),
        },
        {
            value: 'private',
            icon: <LockOutlineIcon size={20}/>,
            title: formatMessage({id: 'docs.spaceSettings.permissions.private.title', defaultMessage: 'Private'}),
            description: formatMessage({id: 'docs.spaceSettings.permissions.private.description', defaultMessage: 'Only invited members can view this space.'}),
            disabled: true,
            disabledReason: formatMessage({id: 'docs.spaceSettings.permissions.private.comingSoon', defaultMessage: 'Coming soon'}),
        },
    ], [formatMessage]);

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
                    value='public'
                    onChange={() => {}}
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
                    disabled={busy}
                />
                <MemberList
                    members={members}
                    avatarSize='sm'
                    spaceTitle={space.title}
                    actions={actions}
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

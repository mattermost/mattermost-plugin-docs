// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useCanManageSpaceMembers} from 'hooks/permissions';
import React, {useCallback} from 'react';
import {useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';

import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import ChevronRightIcon from '@mattermost/compass-icons/components/chevron-right';
import CogOutlineIcon from '@mattermost/compass-icons/components/cog-outline';
import LinkVariantIcon from '@mattermost/compass-icons/components/link-variant';

import {Button} from 'components/form_controls/button';
import {openDocsModal} from 'components/modals';
import SpaceSettingsModal from 'components/space_settings_modal/space_settings_modal';

import type {Space} from 'types/docs';

import styles from './space_info_menu.module.scss';

type ItemProps = {
    icon: React.ReactNode;
    text: string;

    /** Trailing count, as core shows member/file counts. */
    badge?: React.ReactNode;

    /** Marks the item as drilling into a sub-panel, adding a chevron. */
    opensPanel?: boolean;
    onClick: () => void;
};

const SpaceInfoMenuItem = ({icon, text, badge, opensPanel, onClick}: ItemProps) => (
    <Button
        emphasis='quaternary'
        size='sm'
        className={styles.item}
        onClick={onClick}
    >
        <span
            className={styles.icon}
            aria-hidden={true}
        >
            {icon}
        </span>
        <span className={styles.text}>{text}</span>
        {badge !== undefined && <span className={styles.badge}>{badge}</span>}
        {opensPanel && (
            <span
                className={styles.icon}
                aria-hidden={true}
            >
                <ChevronRightIcon size={16}/>
            </span>
        )}
    </Button>
);

type Props = {
    space: Space;
    memberCount?: number;

    /** Drills the panel into its members view. */
    onShowMembers: () => void;
};

/**
 * The space info action list, mirroring core's channel_info_rhs menu: a vertical
 * nav of full-width rows with a leading icon and an optional trailing count.
 */
const SpaceInfoMenu = ({space, memberCount, onShowMembers}: Props) => {
    const {formatMessage} = useIntl();
    const {paths} = useDocsNavigation();
    const canManageMembers = useCanManageSpaceMembers(space.id);

    const openSettings = useCallback(() => {
        openDocsModal((modal) => (
            <SpaceSettingsModal
                space={space}
                onClose={modal.close}
            />
        ));
    }, [space]);

    const copyLink = useCallback(() => {
        copyToClipboard(`${window.location.origin}${paths.space(space.id)}`);
    }, [paths, space.id]);

    return (
        <nav
            className={styles.menu}
            aria-label={formatMessage({id: 'docs.spaceInfo.menu.title', defaultMessage: 'Space info actions'})}
        >
            {canManageMembers && (
                <SpaceInfoMenuItem
                    icon={<CogOutlineIcon size={18}/>}
                    text={formatMessage({id: 'docs.spaceInfo.menu.settings', defaultMessage: 'Space settings'})}
                    onClick={openSettings}
                />
            )}
            <SpaceInfoMenuItem
                icon={<AccountMultipleOutlineIcon size={18}/>}
                text={formatMessage({id: 'docs.spaceInfo.menu.members', defaultMessage: 'Members'})}
                badge={memberCount}
                opensPanel={true}
                onClick={onShowMembers}
            />
            <SpaceInfoMenuItem
                icon={<LinkVariantIcon size={18}/>}
                text={formatMessage({id: 'docs.spaceInfo.menu.copyLink', defaultMessage: 'Copy link'})}
                onClick={copyLink}
            />
        </nav>
    );
};

export default SpaceInfoMenu;

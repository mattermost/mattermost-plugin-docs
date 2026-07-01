// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import MagnifyIcon from '@mattermost/compass-icons/components/magnify';
import {ShortcutKey, ShortcutKeyVariant, ShortcutKeys} from '@mattermost/shared/components/shortcut_key';
import {isMac} from '@mattermost/shared/utils/user_agent';

import styles from './spaces_sidebar_search.module.scss';

type Props = {
    onOpen: () => void;
};

// Opens the Find-docs switcher (Cmd/Ctrl+K) rather than filtering inline,
// mirroring the core channel navigator (SidebarChannelNavigatorButton): the
// shortcut hint is revealed on hover only, and the button uses the same
// sidebar-text hover treatment.
const SpacesSidebarSearch = ({onOpen}: Props) => {
    const {formatMessage} = useIntl();
    const label = formatMessage({id: 'docs.sidebar.search.placeholder', defaultMessage: 'Find docs'});
    const onMac = isMac();

    return (
        <div className={styles.root}>
            <button
                type='button'
                className={styles.field}
                aria-keyshortcuts={onMac ? 'Meta+K' : 'Control+K'}
                aria-haspopup='dialog'
                onClick={onOpen}
            >
                <MagnifyIcon size={18}/>
                <span className={styles.placeholder}>{label}</span>

                {/* Decorative hint (revealed on hover via CSS); the shortcut is
                    advertised to assistive tech via aria-keyshortcuts above. */}
                <span
                    className={styles.shortcut}
                    aria-hidden='true'
                >
                    <ShortcutKey variant={ShortcutKeyVariant.InlineContent}>
                        {onMac ? `${ShortcutKeys.cmd}K` : 'Ctrl+K'}
                    </ShortcutKey>
                </span>
            </button>
        </div>
    );
};

export default SpacesSidebarSearch;

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useSidebarWidth} from 'hooks/sidebar_width';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import ResizableDivider from 'components/resizable_divider/resizable_divider';

import styles from './sidebar.module.scss';

export const DEFAULT_SIDEBAR_WIDTH = 264;
const MIN_SIDEBAR_WIDTH = 200;
const MAX_SIDEBAR_WIDTH = 480;

type Props = {
    open: boolean;
    children: React.ReactNode;
};

/**
 * The space view's left sidebar: a user-resizable panel that slides in and out.
 * The outer element animates its width for the open/close transition while the
 * inner element keeps the full width, so the content is revealed and clipped
 * rather than reflowed mid-slide.
 */
const Sidebar = ({open, children}: Props) => {
    const {formatMessage} = useIntl();
    const {width, setWidth, commitWidth} = useSidebarWidth('pages', DEFAULT_SIDEBAR_WIDTH);
    const [resizing, setResizing] = useState(false);

    return (
        <div
            className={classNames(styles.sidebar, {
                [styles.open]: open,
                [styles.resizing]: resizing,
            })}
            style={{width: open ? width : 0}}
        >
            <div
                className={styles.inner}
                style={{width}}
            >
                {children}
            </div>
            {open && (
                <ResizableDivider
                    ariaLabel={formatMessage({id: 'docs.sidebar.resize', defaultMessage: 'Resize pages sidebar'})}
                    side='left'
                    width={width}
                    minWidth={MIN_SIDEBAR_WIDTH}
                    maxWidth={MAX_SIDEBAR_WIDTH}
                    defaultWidth={DEFAULT_SIDEBAR_WIDTH}
                    onResize={(next) => {
                        setResizing(true);
                        setWidth(next);
                    }}
                    onResizeEnd={(next) => {
                        setResizing(false);
                        commitWidth(next);
                    }}
                />
            )}
        </div>
    );
};

export default Sidebar;

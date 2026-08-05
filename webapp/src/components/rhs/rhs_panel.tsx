// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useSidebarWidth} from 'hooks/sidebar_width';
import React, {useState} from 'react';
import {useIntl} from 'react-intl';

import ChevronLeftIcon from '@mattermost/compass-icons/components/chevron-left';
import CloseIcon from '@mattermost/compass-icons/components/close';

import {Button} from 'components/form_controls/button';
import Header from 'components/header/header';
import ResizableDivider from 'components/resizable_divider/resizable_divider';

import styles from './rhs_panel.module.scss';

const DEFAULT_WIDTH = 400;
const MIN_WIDTH = 304;
const MAX_WIDTH = 776;

type Props = {

    /**
     * The panel's name, stable across its screens. Names the region for a screen
     * reader and stands in for it in the resize and back controls.
     */
    name: string;

    /** The visible heading. Defaults to `name`; a drilled-in screen overrides it. */
    title?: React.ReactNode;

    /** Distinguishes this panel's stored width from other panels'. */
    widthKey: string;

    /** Given only on a drilled-in screen — its presence is what renders Back. */
    onBack?: () => void;
    onClose: () => void;
    children: React.ReactNode;
};

/**
 * The right-hand panel shell, mirroring core's RHS: a resizable full-height column
 * whose header shares the product header chrome, so it lines up with the space
 * header rather than starting below it.
 *
 * Holds the frame only — the width, the resize handle, the heading and the close
 * (and optional back) controls. What the panel is *for* is its children. Open/close
 * state belongs to the caller, which should route it through `useRhs`.
 */
const RhsPanel = ({name, title, widthKey, onBack, onClose, children}: Props) => {
    const {formatMessage} = useIntl();
    const {width, setWidth, commitWidth} = useSidebarWidth(widthKey, DEFAULT_WIDTH);
    const [resizing, setResizing] = useState(false);

    return (
        <aside
            className={classNames(styles.panel, {[styles.resizing]: resizing})}
            style={{width}}
            aria-label={name}
        >
            <ResizableDivider
                ariaLabel={formatMessage({id: 'docs.rhs.resize', defaultMessage: 'Resize {name}'}, {name})}
                side='right'
                width={width}
                minWidth={MIN_WIDTH}
                maxWidth={MAX_WIDTH}
                defaultWidth={DEFAULT_WIDTH}
                onResize={(next) => {
                    setResizing(true);
                    setWidth(next);
                }}
                onResizeEnd={(next) => {
                    setResizing(false);
                    commitWidth(next);
                }}
            />
            <Header
                left={(
                    <>
                        {onBack && (
                            <Button
                                emphasis='quaternary'
                                size='sm'
                                className='btn-icon'
                                aria-label={formatMessage({id: 'docs.rhs.back', defaultMessage: 'Back to {name}'}, {name})}
                                onClick={onBack}
                            >
                                <ChevronLeftIcon size={18}/>
                            </Button>
                        )}
                        <h2 className={styles.headerTitle}>{title ?? name}</h2>
                    </>
                )}
                right={(
                    <Button
                        emphasis='quaternary'
                        size='sm'
                        className='btn-icon'
                        aria-label={formatMessage({id: 'docs.rhs.close', defaultMessage: 'Close'})}
                        onClick={onClose}
                    >
                        <CloseIcon size={18}/>
                    </Button>
                )}
            />
            <div className={styles.body}>{children}</div>
        </aside>
    );
};

export default RhsPanel;

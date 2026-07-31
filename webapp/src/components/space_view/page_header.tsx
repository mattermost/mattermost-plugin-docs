// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useCreateRootPage} from 'hooks/pages';
import {useSidebarWidth} from 'hooks/sidebar_width';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {Timestamp} from 'webapp_globals';
import type {TimestampUnit} from 'webapp_globals';

import ArrowExpandIcon from '@mattermost/compass-icons/components/arrow-expand';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import FormatListBulletedIcon from '@mattermost/compass-icons/components/format-list-bulleted';
import MessageTextOutlineIcon from '@mattermost/compass-icons/components/message-text-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import {Button} from 'components/form_controls/button';
import PageMenu from 'components/page_menu/page_menu';
import Spacer from 'components/spacer/spacer';

import type {Page, Space} from 'types/docs';

import styles from './page_header.module.scss';
import {DEFAULT_SIDEBAR_WIDTH} from './sidebar/sidebar';

// The pages toggle doubles as the accessible label for the page tree section;
// the tree references it via aria-labelledby.
export const PAGES_SECTION_LABEL_ID = 'docsPagesToggle';

// Mirrors `.bar`'s inline-start padding, which the pages cluster has to discount
// from the sidebar width to land on the sidebar's right edge.
const BAR_INLINE_START_PADDING = 8;

// Relative "Updated …" buckets for the host Timestamp.
const UPDATED_TIME_SPEC: TimestampUnit[] = [
    ['minute', -59],
    ['hour', -48],
    ['day', -30],
    ['month', -12],
    'year',
];

type Props = {
    space: Space;

    // The routed page, when one is selected. Drives the "Updated" timestamp and
    // the page-scoped controls; on the space home (no page) only the space's
    // last-updated time shows.
    page?: Page;
    treeOpen: boolean;
    onTogglePages: () => void;
};

// Page controls (comments, edit, overflow, expand) are visual scaffolding wired
// in later passes; the pages toggle drives the page tree panel.
const PageHeader = ({space, page, treeOpen, onTogglePages}: Props) => {
    const {formatMessage} = useIntl();
    const createRootPage = useCreateRootPage(space.id);

    // Read-only view of the pages sidebar's live width, so the add-page button
    // can sit on its right edge (shared store, no prop threading).
    const {width: sidebarWidth} = useSidebarWidth('pages', DEFAULT_SIDEBAR_WIDTH);

    const addPageLabel = formatMessage({id: 'docs.pageTree.add', defaultMessage: 'Add page'});
    const commentsLabel = formatMessage({id: 'docs.space.comments', defaultMessage: 'Comments'});
    const moreLabel = formatMessage({id: 'docs.space.more', defaultMessage: 'More actions'});
    const expandLabel = formatMessage({id: 'docs.space.expand', defaultMessage: 'Expand'});

    // The current page's last-updated time when a page is open, otherwise the
    // space's.
    const updatedAt = page ? page.update_at : space.update_at;

    // Timestamp's `style` is a narrow/short/long format variant, not a DOM style object.
    /* eslint-disable react/style-prop-object */
    const updatedRelative = (
        <Timestamp
            value={updatedAt}
            units={UPDATED_TIME_SPEC}
            useTime={false}
            style='narrow'
        />
    );
    /* eslint-enable react/style-prop-object */

    return (
        <div className={styles.bar}>
            <div
                className={styles.left}
                style={treeOpen ? {width: sidebarWidth - BAR_INLINE_START_PADDING} : undefined}
            >
                <Button
                    id={PAGES_SECTION_LABEL_ID}
                    emphasis='quaternary'
                    size='sm'
                    className={classNames('docs-btn-neutral', styles.iconLabel, {active: treeOpen})}
                    aria-pressed={treeOpen}
                    leadingIcon={<FormatListBulletedIcon size={18}/>}
                    onClick={onTogglePages}
                >
                    <span className={styles.pagesLabel}>
                        <FormattedMessage
                            id='docs.space.pages'
                            defaultMessage='Pages'
                        />
                    </span>
                </Button>
                {treeOpen && <Spacer/>}
                {treeOpen && (
                    <Button
                        emphasis='quaternary'
                        size='sm'
                        className='btn-icon'
                        tooltip={addPageLabel}
                        leadingIcon={<PlusIcon size={18}/>}
                        onClick={createRootPage}
                    />
                )}
            </div>

            <Spacer/>

            <div className={styles.right}>
                <span className={styles.updated}>
                    <FormattedMessage
                        id='docs.space.updated'
                        defaultMessage='Updated {relative}'
                        values={{relative: updatedRelative}}
                    />
                </span>
                {page && (
                    <>
                        <Button
                            emphasis='quaternary'
                            size='sm'
                            className='btn-icon'
                            badge={true}
                            tooltip={commentsLabel}
                            leadingIcon={<MessageTextOutlineIcon size={18}/>}
                        />
                        <Button
                            emphasis='quaternary'
                            size='sm'
                            className={classNames('docs-btn-neutral', styles.iconLabel)}
                            leadingIcon={<PencilOutlineIcon size={18}/>}
                        >
                            <FormattedMessage
                                id='docs.space.edit'
                                defaultMessage='Edit'
                            />
                        </Button>
                        <PageMenu
                            spaceId={space.id}
                            pageId={page.id}
                            pageTitle={page.title}
                            align='right'
                            tooltip={moreLabel}
                            trigger={(
                                <Button
                                    emphasis='quaternary'
                                    size='sm'
                                    className='btn-icon'
                                    aria-label={moreLabel}
                                    leadingIcon={<DotsHorizontalIcon size={18}/>}
                                />
                            )}
                        />
                        <Button
                            emphasis='quaternary'
                            size='sm'
                            className='btn-icon'
                            tooltip={expandLabel}
                            leadingIcon={<ArrowExpandIcon size={18}/>}
                        />
                    </>
                )}
            </div>
        </div>
    );
};

export default PageHeader;

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {Timestamp} from 'webapp_globals';
import type {TimestampUnit} from 'webapp_globals';

import ArrowExpandIcon from '@mattermost/compass-icons/components/arrow-expand';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import FormatListBulletedIcon from '@mattermost/compass-icons/components/format-list-bulleted';
import MessageTextOutlineIcon from '@mattermost/compass-icons/components/message-text-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';

import {Button, SecondaryButton} from 'components/form-controls/button';

import type {Space} from 'types/docs';

import styles from './page_bar.module.scss';

// Relative "Updated …" buckets for the host Timestamp.
const UPDATED_TIME_SPEC: TimestampUnit[] = [
    ['minute', -59],
    ['hour', -48],
    ['day', -30],
    ['month', -12],
    'year',
];

// Controls (pages toggle, comments, edit, overflow, expand) are visual
// scaffolding wired in later passes.
const PageBar = ({space}: {space: Space}) => {
    const {formatMessage} = useIntl();

    const pagesLabel = formatMessage({id: 'docs.space.pages.toggle', defaultMessage: 'Toggle page tree'});
    const commentsLabel = formatMessage({id: 'docs.space.comments', defaultMessage: 'Comments'});
    const moreLabel = formatMessage({id: 'docs.space.more', defaultMessage: 'More actions'});
    const expandLabel = formatMessage({id: 'docs.space.expand', defaultMessage: 'Expand'});

    // Timestamp's `style` is a narrow/short/long format variant, not a DOM style object.
    /* eslint-disable react/style-prop-object */
    const updatedRelative = Timestamp ? (
        <Timestamp
            value={space.update_at}
            units={UPDATED_TIME_SPEC}
            useTime={false}
            style='narrow'
        />
    ) : null;
    /* eslint-enable react/style-prop-object */

    return (
        <div className={styles.bar}>
            <div className={styles.left}>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className={styles.pagesToggle}
                    aria-label={pagesLabel}
                >
                    <FormatListBulletedIcon size={18}/>
                    <span className={styles.pagesLabel}>
                        <FormattedMessage
                            id='docs.space.pages'
                            defaultMessage='Pages'
                        />
                    </span>
                </Button>
            </div>

            <div className={styles.right}>
                {updatedRelative && (
                    <span className={styles.updated}>
                        <FormattedMessage
                            id='docs.space.updated'
                            defaultMessage='Updated {relative}'
                            values={{relative: updatedRelative}}
                        />
                    </span>
                )}
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className={classNames('btn-icon', styles.commentButton)}
                    aria-label={commentsLabel}
                >
                    <MessageTextOutlineIcon size={18}/>
                    <span className={styles.badge}/>
                </Button>
                <SecondaryButton
                    type='button'
                    size='sm'
                    className={styles.edit}
                >
                    <PencilOutlineIcon size={14}/>
                    <FormattedMessage
                        id='docs.space.edit'
                        defaultMessage='Edit'
                    />
                </SecondaryButton>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className='btn-icon'
                    aria-label={moreLabel}
                >
                    <DotsHorizontalIcon size={18}/>
                </Button>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className='btn-icon'
                    aria-label={expandLabel}
                >
                    <ArrowExpandIcon size={16}/>
                </Button>
            </div>
        </div>
    );
};

export default PageBar;

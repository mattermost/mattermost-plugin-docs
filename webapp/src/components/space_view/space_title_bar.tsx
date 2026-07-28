// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';

import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import InformationOutlineIcon from '@mattermost/compass-icons/components/information-outline';
import ShareVariantOutlineIcon from '@mattermost/compass-icons/components/share-variant-outline';
import StarOutlineIcon from '@mattermost/compass-icons/components/star-outline';

import {Button, PrimaryButton} from 'components/form-controls/button';
import ShareSpaceModal from 'components/share_space_modal/share_space_modal';

import type {Space} from 'types/docs';

import styles from './space_title_bar.module.scss';

// The controls here (favorite, space menu, details, share) are visual
// scaffolding — wired in later passes. Icon buttons use the shared Button with
// the compass `btn-icon` treatment (quaternary + square), the same as core.
const SpaceTitleBar = ({space, memberCount}: {space: Space; memberCount: number}) => {
    const {formatMessage} = useIntl();
    const [shareOpen, setShareOpen] = useState(false);

    const favoriteLabel = formatMessage({id: 'docs.space.favorite', defaultMessage: 'Favorite this space'});
    const menuLabel = formatMessage({id: 'docs.space.menu', defaultMessage: 'Space options'});
    const detailsLabel = formatMessage({id: 'docs.space.details', defaultMessage: 'Space details'});

    return (
        <div className={styles.bar}>
            <div className={styles.left}>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className='btn-icon'
                    aria-label={favoriteLabel}
                >
                    <StarOutlineIcon size={18}/>
                </Button>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className={styles.titleTrigger}
                    aria-label={menuLabel}
                >
                    <span
                        className={styles.emoji}
                        aria-hidden={true}
                    >
                        <SpaceIcon
                            space={space}
                            size={18}
                        />
                    </span>
                    <span className={styles.title}>{space.title}</span>
                    <ChevronDownIcon
                        className={styles.chevron}
                        size={16}
                    />
                </Button>
                <span className={styles.members}>
                    <AccountMultipleOutlineIcon size={16}/>
                    <span>{memberCount}</span>
                </span>
            </div>

            <div className={styles.right}>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className='btn-icon'
                    aria-label={detailsLabel}
                >
                    <InformationOutlineIcon size={18}/>
                </Button>
                <PrimaryButton
                    type='button'
                    size='sm'
                    className={styles.share}
                    onClick={() => setShareOpen(true)}
                >
                    <ShareVariantOutlineIcon size={16}/>
                    <FormattedMessage
                        id='docs.space.share'
                        defaultMessage='Share'
                    />
                </PrimaryButton>
            </div>
            {shareOpen && (
                <ShareSpaceModal
                    space={space}
                    onClose={() => setShareOpen(false)}
                />
            )}
        </div>
    );
};

export default SpaceTitleBar;

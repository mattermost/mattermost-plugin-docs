// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';

import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import InformationOutlineIcon from '@mattermost/compass-icons/components/information-outline';
import ShareVariantOutlineIcon from '@mattermost/compass-icons/components/share-variant-outline';
import StarOutlineIcon from '@mattermost/compass-icons/components/star-outline';

import {PrimaryButton} from 'components/form-controls/button';

import type {Space} from 'types/docs';

import styles from './space_title_bar.module.scss';

// The controls here (favorite, space menu, details, share) are visual scaffolding
// — wired in later passes. Member count awaits the space-members API.
const SpaceTitleBar = ({space}: {space: Space}) => {
    const {formatMessage} = useIntl();

    const favoriteLabel = formatMessage({id: 'docs.space.favorite', defaultMessage: 'Favorite this space'});
    const menuLabel = formatMessage({id: 'docs.space.menu', defaultMessage: 'Space options'});
    const detailsLabel = formatMessage({id: 'docs.space.details', defaultMessage: 'Space details'});

    return (
        <div className={styles.bar}>
            <div className={styles.left}>
                <button
                    type='button'
                    className={styles.iconButton}
                    aria-label={favoriteLabel}
                >
                    <StarOutlineIcon size={18}/>
                </button>
                <button
                    type='button'
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
                </button>
                <span className={styles.members}>
                    <AccountMultipleOutlineIcon size={16}/>

                    {/* eslint-disable-next-line formatjs/no-literal-string-in-jsx -- em dash placeholder until the members API is wired */}
                    <span>{'—'}</span>
                </span>
            </div>

            <div className={styles.right}>
                <button
                    type='button'
                    className={styles.iconButton}
                    aria-label={detailsLabel}
                >
                    <InformationOutlineIcon size={18}/>
                </button>
                <PrimaryButton type='button'>
                    <span className={styles.share}>
                        <ShareVariantOutlineIcon size={16}/>
                        <FormattedMessage
                            id='docs.space.share'
                            defaultMessage='Share'
                        />
                    </span>
                </PrimaryButton>
            </div>
        </div>
    );
};

export default SpaceTitleBar;

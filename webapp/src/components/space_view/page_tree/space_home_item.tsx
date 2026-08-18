// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useDocsNavigation} from 'hooks/navigation';
import React from 'react';
import {useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';

import type {Space} from 'types/docs';

import styles from './space_home_item.module.scss';

type Props = {
    space: Space;
};

/**
 * The space's front door, above the page tree. Sits outside the tree's
 * `role="tree"` — it isn't a page — so it's a plain button rather than a
 * treeitem, and reads as selected whenever no page is routed.
 */
const SpaceHomeItem = ({space}: Props) => {
    const {formatMessage} = useIntl();
    const {pageId, goToOverview} = useDocsNavigation();
    const active = !pageId;

    return (
        <button
            type='button'
            className={classNames(styles.row, {[styles.active]: active})}
            aria-current={active ? 'page' : undefined}
            onClick={() => goToOverview(space.id)}
        >
            <span
                className={styles.chevronSpacer}
                aria-hidden={true}
            />
            <span
                className={styles.icon}
                aria-hidden={true}
            >
                <SpaceIcon
                    space={space}
                    size={16}
                />
            </span>
            <span className={styles.title}>
                {formatMessage({id: 'docs.pageTree.overview', defaultMessage: 'Overview'})}
            </span>
        </button>
    );
};

export default SpaceHomeItem;

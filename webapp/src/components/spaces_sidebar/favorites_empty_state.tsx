// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React, {useState} from 'react';
import {FormattedMessage} from 'react-intl';

import {useFavoritesDropZone} from './dnd/use_favorites_drop_zone';
import styles from './favorites_empty_state.module.scss';

const FavoritesEmptyState = () => {
    const [element, setElement] = useState<HTMLDivElement | null>(null);
    const over = useFavoritesDropZone(element);

    return (
        <div className={styles.root}>
            <div
                ref={setElement}
                className={classNames(styles.box, {[styles.over]: over})}
            >
                <FormattedMessage
                    id='docs.sidebar.favorites.empty'
                    defaultMessage='Drag favorite items here or click the star icon on any space'
                />
            </div>
        </div>
    );
};

export default FavoritesEmptyState;

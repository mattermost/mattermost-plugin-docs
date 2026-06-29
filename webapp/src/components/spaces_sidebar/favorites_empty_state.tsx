// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React, {useState} from 'react';
import {FormattedMessage} from 'react-intl';

import {useFavoritesDropZone} from './dnd/use_favorites_drop_zone';
import './favorites_empty_state.scss';

const FavoritesEmptyState = () => {
    const [element, setElement] = useState<HTMLDivElement | null>(null);
    const over = useFavoritesDropZone(element);

    return (
        <div className='DocsFavoritesEmpty'>
            <div
                ref={setElement}
                className={classNames('DocsFavoritesEmpty__box', {'DocsFavoritesEmpty__box--over': over})}
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

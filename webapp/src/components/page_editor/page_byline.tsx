// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {siteRoot} from 'client/rest';
import React, {useEffect} from 'react';
import {FormattedMessage} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';

import type {GlobalState} from '@mattermost/types/store';

import {getMissingProfilesByIds} from 'mattermost-redux/actions/users';
import {getTeammateNameDisplaySetting} from 'mattermost-redux/selectors/entities/preferences';
import {getUser} from 'mattermost-redux/selectors/entities/users';
import {displayUsername} from 'mattermost-redux/utils/user_utils';

import styles from './page_byline.module.scss';

type Props = {
    userId: string;
};

const PageByline = ({userId}: Props) => {
    const dispatch = useDispatch();
    const author = useSelector((state: GlobalState) => getUser(state, userId));
    const teammateNameDisplay = useSelector(getTeammateNameDisplaySetting) || '';

    useEffect(() => {
        if (userId && !author) {
            dispatch(getMissingProfilesByIds([userId]) as never);
        }
    }, [dispatch, userId, author]);

    if (!author) {
        return null;
    }

    return (
        <div className={styles.root}>
            <img
                className={styles.avatar}
                src={`${siteRoot()}/api/v4/users/${userId}/image?_=${author.last_picture_update ?? 0}`}
                alt=''
            />
            <span className={styles.name}>
                <FormattedMessage
                    id='docs.editor.byline'
                    defaultMessage='By {name}'
                    values={{name: displayUsername(author, teammateNameDisplay)}}
                />
            </span>
        </div>
    );
};

export default PageByline;

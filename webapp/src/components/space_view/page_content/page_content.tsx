// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useUserProfile} from 'hooks/members';
import {useAppSelector} from 'hooks/redux';
import React from 'react';
import {useIntl} from 'react-intl';
import {Avatar} from 'webapp_globals';

import EmoticonPlusOutlineIcon from '@mattermost/compass-icons/components/emoticon-plus-outline';
import ImageOutlineIcon from '@mattermost/compass-icons/components/image-outline';

import {getPage} from 'store/selectors';

import {Button} from 'components/form_controls/button';

import type {Page} from 'types/docs';

import styles from './page_content.module.scss';
import PageContentPlaceholder from './page_content_placeholder';

type Props = {
    pageId: string;
};

// The selected page's content column: a title area over the page body. The body
// is a skeleton until the editor is mounted, which also covers the window where
// the routed page hasn't arrived in the store yet (no title to show).
const PageContent = ({pageId}: Props) => {
    const page = useAppSelector((state) => getPage(state, pageId));

    return (
        <div className={styles.frame}>
            <article className={styles.article}>
                {page && <PageTitleArea page={page}/>}
                <PageContentPlaceholder/>
            </article>
        </div>
    );
};

// Title, byline, and the decoration actions that reveal on hover. Adding an
// emoji or a header image needs page props the editor work will bring, so those
// two are affordances only for now.
const PageTitleArea = ({page}: {page: Page}) => {
    const {formatMessage} = useIntl();
    const author = useUserProfile(page.user_id);

    return (
        <header className={styles.titleArea}>
            <div className={styles.topActions}>
                <Button
                    emphasis='quaternary'
                    size='xs'
                    className={styles.topAction}
                    leadingIcon={<EmoticonPlusOutlineIcon size={12}/>}
                >
                    {formatMessage({id: 'docs.page.addEmoji', defaultMessage: 'Add emoji'})}
                </Button>
                <Button
                    emphasis='quaternary'
                    size='xs'
                    className={styles.topAction}
                    leadingIcon={<ImageOutlineIcon size={12}/>}
                >
                    {formatMessage({id: 'docs.page.headerImage', defaultMessage: 'Header image'})}
                </Button>
            </div>

            <h1 className={classNames(styles.title, {[styles.titleUntitled]: !page.title})}>
                {page.title || formatMessage({id: 'docs.page.untitled', defaultMessage: 'Untitled'})}
            </h1>

            {author?.displayName && (
                <div className={styles.metadata}>
                    <span className={styles.chip}>
                        <span
                            className={styles.chipAvatar}
                            aria-hidden={true}
                        >
                            <Avatar
                                url={author.avatarUrl}
                                username={author.username}
                                size='xxs'
                                name=''
                            />
                        </span>
                        {formatMessage({id: 'docs.page.byAuthor', defaultMessage: 'By {name}'}, {name: author.displayName})}
                    </span>
                </div>
            )}
        </header>
    );
};

export default PageContent;

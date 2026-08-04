// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useUserProfile} from 'hooks/members';
import {useDocsNavigation} from 'hooks/navigation';
import {useAppDispatch} from 'hooks/redux';
import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {Avatar} from 'webapp_globals';

import EmoticonPlusOutlineIcon from '@mattermost/compass-icons/components/emoticon-plus-outline';
import ImageOutlineIcon from '@mattermost/compass-icons/components/image-outline';

import {updatePage} from 'store/actions';

import {Button} from 'components/form_controls/button';
import PageEditor from 'components/page_editor/page_editor';
import {toast} from 'components/toast';

import type {Page} from 'types/docs';

import styles from './page_content.module.scss';
import PageContentPlaceholder from './page_content_placeholder';
import PageTitle from './page_title';

type Props = {

    // Resolved and space-validated by the caller; undefined only while the
    // space's pages are still loading (an id that doesn't belong to the space
    // redirects instead of rendering here).
    page?: Page;
    editing: boolean;
};

// The selected page's content column: a title area over the page body, which is
// the editor. Until the routed page arrives in the store there is no title and no
// id to open the editor on, so that window stays a skeleton.
const PageContent = ({page, editing}: Props) => {
    const {isDraft} = useDocsNavigation();

    return (
        <div className={styles.frame}>
            <article className={styles.article}>
                {page ? (
                    <>
                        <PageTitleArea
                            page={page}
                            editing={editing}
                        />
                        <PageEditor
                            spaceId={page.space_id}
                            pageId={page.id}
                            isDraft={isDraft}
                        />
                    </>
                ) : (
                    <PageContentPlaceholder/>
                )}
            </article>
        </div>
    );
};

// Title, byline, and the decoration actions that reveal on hover. Adding an
// emoji or a header image needs page props the editor work will bring, so those
// two are affordances only for now.
const PageTitleArea = ({page, editing}: {page: Page; editing: boolean}) => {
    const {formatMessage} = useIntl();
    const author = useUserProfile(page.user_id);
    const [title, setTitle] = useState(page.title);
    const dispatch = useAppDispatch();

    // Enter and blur are independent triggers (Enter doesn't blur the field), so
    // both can fire commit before a prior write resolves and the store's page.title
    // catches up. Without this guard a second write goes out on the same stale
    // baseline — wasted at best, a spurious failure toast at worst if the first
    // write's edit_at bump turns the second into a conflict.
    const commitInFlight = useRef(false);

    // A title edited elsewhere (the rename modal, another client) replaces the
    // buffer; the routed page changing does too, since the component is reused.
    useEffect(() => setTitle(page.title), [page.id, page.title]);

    // Trailing whitespace is never intentional in a title, and a title that only
    // changed by whitespace is not a change worth a write.
    const commit = async () => {
        const next = title.trim();
        setTitle(next);
        if (next === page.title || commitInFlight.current) {
            return;
        }

        commitInFlight.current = true;
        try {
            await dispatch(updatePage(page.space_id, page.id, {title: next}));
        } catch {
            // Keep the typed title: reverting would look like the edit was taken
            // and then silently lost. The rejection reason itself isn't
            // actionable here, so it's deliberately dropped rather than logged.
            toast.error(formatMessage({
                id: 'docs.page.titleSaveFailed',
                defaultMessage: 'Could not rename the page. Please try again.',
            }));
        } finally {
            commitInFlight.current = false;
        }
    };

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

            <PageTitle
                value={title}
                editing={editing}
                onChange={setTitle}
                onCommit={commit}
                onCancel={() => setTitle(page.title)}
            />

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

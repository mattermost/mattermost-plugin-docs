// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEditBaseline} from 'hooks/edit_baseline';
import {useUserProfile} from 'hooks/members';
import {useAppDispatch} from 'hooks/redux';
import React, {useCallback, useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {Avatar} from 'webapp_globals';

import EmoticonPlusOutlineIcon from '@mattermost/compass-icons/components/emoticon-plus-outline';
import ImageOutlineIcon from '@mattermost/compass-icons/components/image-outline';

import {saveDraft, updatePage} from 'store/actions';

import {Button} from 'components/form_controls/button';
import PageEditor from 'components/page_editor/page_editor';
import {toast} from 'components/toast';

import {UNTITLED_PAGE_TITLE} from 'types/docs';
import type {Page} from 'types/docs';
import type {Draft} from 'types/drafts';

import styles from './page_content.module.scss';
import PageContentPlaceholder from './page_content_placeholder';
import PageTitle from './page_title';

type Props = {

    // The routed page, when it is published. Undefined while the space's pages are
    // still loading (an id that doesn't belong to the space redirects instead of
    // rendering here), and for a draft that has no published page yet.
    page?: Page;

    // The routed draft, for an unpublished page. Mutually exclusive with `page` in
    // practice: unpublished edits to a published page render the page.
    draft?: Draft;
    editing: boolean;
};

// What the title area needs, from whichever of the two backs this view. A draft is
// addressed by the page id it reserved, so `id` means the same thing either way.
type TitleSubject = {
    id: string;
    spaceId: string;
    title: string;
    authorId: string;
};

// The selected page's content column: a title area over the page body, which is the
// editor. Until the routed page or draft arrives in the store there is no title and
// no id to open the editor on, so that window stays a skeleton.
const PageContent = ({page, draft, editing}: Props) => {
    const dispatch = useAppDispatch();

    const baseEditAt = useEditBaseline(page?.id ?? '', page?.edit_at);

    const subject: TitleSubject | undefined = page ? {
        id: page.id,
        spaceId: page.space_id,

        title: (editing && draft?.title) || page.title,
        authorId: page.user_id,
    } : draft && {
        id: draft.page_id,
        spaceId: draft.space_id,
        title: draft.title,
        authorId: draft.user_id,
    };

    // A published page's title is patched on the page; an unpublished one is an
    // autosave against the draft. Same gesture, different endpoint — so the title
    // area takes the write as a callback rather than knowing which it is.
    const saveTitle = useCallback(async (title: string) => {
        if (page && (editing || draft)) {
            await dispatch(saveDraft(page.space_id, page.id, {title, base_edit_at: baseEditAt}));
        } else if (page) {
            await dispatch(updatePage(page.space_id, page.id, {title}));
        } else if (draft) {
            await dispatch(saveDraft(draft.space_id, draft.page_id, {title}));
        }
    }, [dispatch, page, draft, editing, baseEditAt]);

    return (
        <div className={styles.frame}>
            <article className={styles.article}>
                {subject ? (
                    <>
                        {/* The title buffer belongs to one page: keying it means a
                            newly routed page never inherits what was typed for the
                            last one, and a write already in flight keeps targeting
                            the page it started on. */}
                        <PageTitleArea
                            key={subject.id}
                            subject={subject}
                            editing={editing}
                            saveTitle={saveTitle}
                        />
                        <PageEditor
                            spaceId={subject.spaceId}
                            pageId={subject.id}
                            isDraft={!page}
                            editing={editing}
                        />
                    </>
                ) : (
                    <PageContentPlaceholder/>
                )}
            </article>
        </div>
    );
};

type TitleAreaProps = {
    subject: TitleSubject;
    editing: boolean;
    saveTitle: (title: string) => Promise<void>;
};

// Title, byline, and the decoration actions that reveal on hover. Adding an emoji or
// a header image needs page props the editor work will bring, so those two are
// affordances only for now.
const PageTitleArea = ({subject, editing, saveTitle}: TitleAreaProps) => {
    const {formatMessage} = useIntl();
    const author = useUserProfile(subject.authorId);

    const untitled = formatMessage(UNTITLED_PAGE_TITLE);

    // An unnamed page stores the untitled placeholder as its title, because the
    // server has no empty-title representation. The field shows that as nothing at
    // all, so typing a name replaces it without selecting it first; `asStored` maps
    // back for the write.
    const asBuffer = useCallback((stored: string) => (stored === untitled ? '' : stored), [untitled]);
    const asStored = (buffer: string) => buffer.trim() || untitled;

    const [title, setTitle] = useState(() => asBuffer(subject.title));

    // Commit runs from an effect cleanup and from a settled write, neither of which
    // can see the render they were created in; the ref is the buffer as it is now.
    const titleRef = useRef(title);
    const setBuffer = useCallback((next: string) => {
        titleRef.current = next;
        setTitle(next);
    }, []);

    // Enter and blur are independent triggers (Enter doesn't blur the field), so
    // both can fire commit before a prior write resolves and the stored title
    // catches up. Without this guard a second write goes out on the same stale
    // baseline — wasted at best, a spurious failure toast at worst if the first
    // write's edit_at bump turns the second into a conflict.
    const commitInFlight = useRef(false);

    // The title this component last took from the store. The buffer holding anything
    // else means there is unsaved input, which an incoming title must not replace —
    // including the title our own write just put in the store, since the user may
    // have typed on while it was in flight.
    const observedTitle = useRef(asBuffer(subject.title));
    useEffect(() => {
        const previous = observedTitle.current;
        observedTitle.current = asBuffer(subject.title);
        if (titleRef.current === previous) {
            setBuffer(observedTitle.current);
        }
    }, [subject.title, asBuffer, setBuffer]);

    // Deferred callers reach commit through the ref so they run the current one,
    // which reads today's stored title rather than the one their closure captured.
    const commitRef = useRef<() => Promise<void>>();

    // Trailing whitespace is never intentional in a title, and a title that only
    // changed by whitespace is not a change worth a write. Clearing the field is a
    // return to unnamed rather than an error: it stores the placeholder, and the
    // field stays empty because that is how unnamed reads here.
    const commit = async () => {
        const typed = titleRef.current.trim();
        if (typed !== titleRef.current) {
            setBuffer(typed);
        }
        const next = asStored(typed);
        if (next === subject.title || commitInFlight.current) {
            return;
        }

        commitInFlight.current = true;
        try {
            await saveTitle(next);
        } catch {
            // Keep the typed title: reverting would look like the edit was taken
            // and then silently lost. The rejection reason itself isn't actionable
            // here, so it's deliberately dropped rather than logged.
            toast.error(formatMessage({
                id: 'docs.page.titleSaveFailed',
                defaultMessage: 'Could not rename the page. Please try again.',
            }));
        } finally {
            commitInFlight.current = false;
        }

        // Anything typed while that write was in flight was turned away by the guard
        // above and would otherwise be lost. One extra write per round trip at most,
        // and the last thing typed is the one that lands.
        if (titleRef.current.trim() !== typed) {
            await commitRef.current?.();
        }
    };
    commitRef.current = commit;

    // Leaving edit mode is a commit trigger like blur and Enter: unmounting the
    // field raises no blur event, so exits that don't go through the field — Back,
    // routing to another page — would otherwise drop what was typed. Committing an
    // unchanged title is already a no-op, so a blur that beat us here writes once.
    useEffect(() => {
        if (!editing) {
            return undefined;
        }
        return () => {
            commitRef.current?.();
        };
    }, [editing]);

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

            {/* Reading mode shows the stored title, not the buffer: a commit that
                failed keeps the typed value in the field for another try, but the
                heading must not present it as the page's title. */}
            <PageTitle
                value={editing ? title : subject.title}
                editing={editing}
                onChange={setBuffer}
                onCommit={commit}
                onCancel={() => setBuffer(asBuffer(subject.title))}
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

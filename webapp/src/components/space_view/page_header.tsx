// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useAutosaveStatus} from 'hooks/autosave_status';
import {useFullscreen} from 'hooks/fullscreen';
import {usePagePresence} from 'hooks/page_presence';
import {useCreateRootPage} from 'hooks/pages';
import {useCanCreatePage, useCanEditPage} from 'hooks/permissions';
import {useSidebarWidth} from 'hooks/sidebar_width';
import {useCurrentUserId} from 'hooks/user';
import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FormattedMessage, defineMessage, useIntl} from 'react-intl';
import {Timestamp} from 'webapp_globals';
import type {TimestampUnit} from 'webapp_globals';

import ArrowCollapseIcon from '@mattermost/compass-icons/components/arrow-collapse';
import ArrowExpandIcon from '@mattermost/compass-icons/components/arrow-expand';
import CheckIcon from '@mattermost/compass-icons/components/check';
import DotsHorizontalIcon from '@mattermost/compass-icons/components/dots-horizontal';
import FormatListBulletedIcon from '@mattermost/compass-icons/components/format-list-bulleted';
import MessageTextOutlineIcon from '@mattermost/compass-icons/components/message-text-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import {isMac} from '@mattermost/shared/utils/user_agent';

import {Button} from 'components/form_controls/button';
import AutosaveIndicator from 'components/page_editor/autosave_indicator';
import PageMenu from 'components/page_menu/page_menu';
import Spacer from 'components/spacer/spacer';
import {toast} from 'components/toast';

import type {Page, Space} from 'types/docs';
import type {Draft} from 'types/drafts';

import styles from './page_header.module.scss';
import {DEFAULT_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH, MIN_SIDEBAR_WIDTH} from './sidebar/sidebar';

// The pages toggle doubles as the accessible label for the page tree section;
// the tree references it via aria-labelledby.
export const PAGES_SECTION_LABEL_ID = 'docsPagesToggle';

// The page tree's keyboard instructions live in the tree panel but are referenced
// from the toggle too: `aria-describedby` is only announced when the described
// element takes focus, and the tree container never does — focus always lands on a
// row. The toggle is the focusable thing right before the tree, so describing it
// is what actually reaches a screen reader on the way in.
export const PAGES_KEYBOARD_HELP_ID = 'docsPageTreeHelp';

// Mirrors `.bar`'s inline-start padding, which the pages cluster has to discount
// from the sidebar width to land on the sidebar's right edge.
const BAR_INLINE_START_PADDING = 8;

// Descriptors rather than inline objects: the expand control picks between them, and
// a bare object inside a conditional isn't statically analyzable, so extraction would
// miss both.
const EXPAND_LABEL = defineMessage({id: 'docs.space.expand', defaultMessage: 'Expand'});
const EXIT_FULLSCREEN_LABEL = defineMessage({id: 'docs.space.exitFullscreen', defaultMessage: 'Exit fullscreen'});

// Relative "Updated …" buckets for the host Timestamp.
const UPDATED_TIME_SPEC: TimestampUnit[] = [
    ['minute', -59],
    ['hour', -48],
    ['day', -30],
    ['month', -12],
    'year',
];

type Props = {
    space: Space;

    // The routed page, when one is selected. Drives the "Updated" timestamp and
    // the page-scoped controls; on the space home (no page) only the space's
    // last-updated time shows.
    page?: Page;

    // The routed page's draft, if it has one. With no `page` it is an unpublished
    // page and Publish creates it; alongside a `page` it is unpublished edits and
    // Update applies them.
    draft?: Draft;
    treeOpen: boolean;
    editing: boolean;
    commentsOpen: boolean;
    onTogglePages: () => void;
    onToggleComments: () => void;
    onToggleEdit: () => void;
    onPublish: () => void;
};

// Overflow is visual scaffolding wired in a later pass; the pages toggle drives the
// page tree panel, Comments opens its right-hand panel, expand goes fullscreen, and
// Edit/Close plus Publish/Update are live.
const PageHeader = ({space, page, draft, treeOpen, editing, commentsOpen, onTogglePages, onToggleComments, onToggleEdit, onPublish}: Props) => {
    const {formatMessage} = useIntl();
    const createRootPage = useCreateRootPage(space.id);

    // Withheld rather than disabled, as the space's other unauthorized actions are
    // (Space settings, Archive space): an action the server will refuse is not offered.
    const canCreatePage = useCanCreatePage(space.id);

    // Editing an already-published page commits through edit_page, which a space default can
    // withhold independently of create_page (see PublishPageDraft's split gate). Withheld the same
    // way page creation is, rather than disabled: an action the server will refuse is not offered.
    const canEditPage = useCanEditPage(space.id);
    const autosaveStatus = useAutosaveStatus();

    // Read here rather than passed in: the sidebar it hides belongs to the product
    // root, which shares no state with this header — see useFullscreen.
    const {fullscreen, toggleFullscreen} = useFullscreen();
    const [publishing, setPublishing] = useState(false);

    // Publishing navigates on success, so the guard is against a second click during
    // the round trip rather than a state this component returns to.
    const publish = useCallback(async () => {
        setPublishing(true);
        try {
            await onPublish();
        } finally {
            setPublishing(false);
        }
    }, [onPublish]);

    // Read-only view of the pages sidebar's live width, so the add-page button
    // can sit on its right edge (shared store, no prop threading).
    const {width: sidebarWidth} = useSidebarWidth('pages', DEFAULT_SIDEBAR_WIDTH, {
        minWidth: MIN_SIDEBAR_WIDTH,
        maxWidth: MAX_SIDEBAR_WIDTH,
    });

    const addPageLabel = formatMessage({id: 'docs.pageTree.add', defaultMessage: 'Add page'});
    const commentsLabel = formatMessage({id: 'docs.space.comments', defaultMessage: 'Comments'});
    const moreLabel = formatMessage({id: 'docs.space.more', defaultMessage: 'More actions'});
    const expandLabel = formatMessage(fullscreen ? EXIT_FULLSCREEN_LABEL : EXPAND_LABEL);
    const updateLabel = formatMessage({id: 'docs.space.update', defaultMessage: 'Update'});
    const noChangesLabel = formatMessage({id: 'docs.space.noUnpublishedChanges', defaultMessage: 'No unpublished changes'});

    // What the page-scoped controls act on: the published page, or the unpublished
    // one when there is no published version yet.
    const subject = page ?? (draft && {id: draft.page_id, title: draft.title});

    // Two different states share one draft: with no published page it *is* the page
    // (Publish creates it); with one, it is unpublished edits to it (Update applies
    // them).
    const unpublished = !page && Boolean(draft);
    const hasUnpublishedEdits = Boolean(page) && Boolean(draft);

    // An unpublished page is the caller's own draft, and publishing it for the first time is gated
    // on create_page rather than edit_page — so it stays editable and publishable for an author who
    // holds only the former.
    const canAuthor = unpublished ? canCreatePage : canEditPage;
    const canEnterEdit = Boolean(subject) && !editing && canAuthor;
    const canCommit = (unpublished && canCreatePage) || (editing && hasUnpublishedEdits && canEditPage);
    const commitShortcut = isMac() ? 'Meta+Enter' : 'Control+Enter';

    // Capture so Cmd/Ctrl+Enter wins over the editor inserting a newline, and
    // over the title field treating Enter as "done". `e` stays off form fields
    // so typing still works while editing.
    useEffect(() => {
        if (!canEnterEdit) {
            return undefined;
        }

        const onKeyDown = (event: KeyboardEvent) => {
            if (event.key !== 'e' && event.key !== 'E') {
                return;
            }
            if (event.metaKey || event.ctrlKey || event.altKey) {
                return;
            }

            const target = event.target as HTMLElement | null;
            if (target?.closest?.('[role="dialog"]')) {
                return;
            }
            const tag = target?.tagName;
            if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target?.isContentEditable) {
                return;
            }

            event.preventDefault();
            onToggleEdit();
        };

        document.addEventListener('keydown', onKeyDown);
        return () => {
            document.removeEventListener('keydown', onKeyDown);
        };
    }, [canEnterEdit, onToggleEdit]);

    useEffect(() => {
        if (!canCommit || publishing) {
            return undefined;
        }

        const onKeyDown = (event: KeyboardEvent) => {
            if (event.key !== 'Enter' || !(event.metaKey || event.ctrlKey)) {
                return;
            }
            if ((event.target as HTMLElement | null)?.closest?.('[role="dialog"]')) {
                return;
            }

            event.preventDefault();
            event.stopPropagation();
            publish().catch(() => undefined);
        };

        document.addEventListener('keydown', onKeyDown, true);
        return () => {
            document.removeEventListener('keydown', onKeyDown, true);
        };
    }, [canCommit, publishing, publish]);

    // canCommit depends on canEditPage, so Update disappears when the grant is revoked
    // while Close (which does not gate on canAuthor) stays; without this notice the
    // editor is left mid-edit with no explanation of why the control went away.
    const wasEditablePage = useRef(canEditPage);
    useEffect(() => {
        const wasEditable = wasEditablePage.current;
        wasEditablePage.current = canEditPage;

        if (editing && wasEditable && !canEditPage) {
            toast.error(formatMessage({
                id: 'docs.space.editRevoked',
                defaultMessage: 'Your permission to edit this page was removed.',
            }));
        }
    }, [canEditPage, editing, formatMessage]);

    // The open page's last-updated time, the draft's own while it is unpublished,
    // otherwise the space's.
    const updatedAt = page?.update_at ?? draft?.update_at ?? space.update_at;

    const currentUserId = useCurrentUserId();
    const activeEditors = usePagePresence(space.id, subject?.id ?? '', currentUserId);

    // Timestamp's `style` is a narrow/short/long format variant, not a DOM style object.
    /* eslint-disable react/style-prop-object */
    const updatedRelative = (
        <Timestamp
            value={updatedAt}
            units={UPDATED_TIME_SPEC}
            useTime={false}
            style='narrow'
        />
    );
    /* eslint-enable react/style-prop-object */

    return (
        <div className={styles.bar}>
            <div
                className={styles.left}
                style={treeOpen ? {width: sidebarWidth - BAR_INLINE_START_PADDING} : undefined}
            >
                <Button
                    id={PAGES_SECTION_LABEL_ID}
                    aria-describedby={treeOpen ? PAGES_KEYBOARD_HELP_ID : undefined}
                    emphasis='quaternary'
                    size='sm'
                    className={classNames('docs-btn-neutral', styles.iconLabel, {active: treeOpen})}
                    aria-pressed={treeOpen}
                    leadingIcon={<FormatListBulletedIcon size={18}/>}
                    onClick={onTogglePages}
                >
                    <span className={styles.pagesLabel}>
                        <FormattedMessage
                            id='docs.space.pages'
                            defaultMessage='Pages'
                        />
                    </span>
                </Button>
                {treeOpen && <Spacer/>}
                {treeOpen && canCreatePage && (
                    <Button
                        emphasis='quaternary'
                        size='sm'
                        className='btn-icon docs-btn-neutral'
                        tooltip={addPageLabel}
                        leadingIcon={<PlusIcon size={18}/>}
                        onClick={createRootPage}
                    />
                )}
            </div>

            <Spacer/>

            <div className={styles.right}>
                {activeEditors.length > 0 && (
                    <span className={styles.updated}>
                        <FormattedMessage
                            id='docs.space.otherEditors'
                            defaultMessage='{count, plural, one {# other editor} other {# other editors}}'
                            values={{count: activeEditors.length}}
                        />
                    </span>
                )}
                {autosaveStatus ? (
                    <AutosaveIndicator status={autosaveStatus}/>
                ) : (
                    <span className={styles.updated}>
                        <FormattedMessage
                            id='docs.space.updated'
                            defaultMessage='Updated {relative}'
                            values={{relative: updatedRelative}}
                        />
                    </span>
                )}
                {page && (
                    <Button
                        emphasis='quaternary'
                        size='sm'
                        className={classNames('btn-icon', 'docs-btn-neutral', {active: commentsOpen})}
                        badge={true}
                        tooltip={commentsLabel}
                        aria-pressed={commentsOpen}
                        leadingIcon={<MessageTextOutlineIcon size={18}/>}
                        onClick={onToggleComments}
                    />
                )}

                {/* Publish/Update sits before Close: the action that commits the
                    work reads first, and leaving is the fallback beside it.

                    Publish shows in both modes — an unpublished page is invisible to
                    everyone else until it lands, so that stays offered while reading
                    it. Update is an editing action: with the page already published
                    there is nothing urgent to offer a reader. */}
                {unpublished ? (canCreatePage && (
                    <Button
                        emphasis='primary'
                        size='sm'
                        disabled={publishing}
                        aria-keyshortcuts={commitShortcut}
                        onClick={publish}
                    >
                        <FormattedMessage
                            id='docs.space.publish'
                            defaultMessage='Publish'
                        />
                    </Button>
                )) : (editing && subject && canEditPage && (
                    <Button
                        emphasis='primary'
                        size='sm'

                        // Update publishes the edits held as a draft against this
                        // page. With none there is nothing to apply, so the control
                        // says why rather than failing when pressed.
                        disabled={publishing || !hasUnpublishedEdits}

                        // Explicit, because `tooltip` otherwise becomes the
                        // accessible name — and the control is still "Update"
                        // whatever the reason it is unavailable.
                        aria-label={updateLabel}
                        aria-keyshortcuts={commitShortcut}
                        tooltip={hasUnpublishedEdits ? undefined : noChangesLabel}
                        onClick={publish}
                    >
                        <FormattedMessage
                            id='docs.space.update'
                            defaultMessage='Update'
                        />
                    </Button>
                ))}

                {subject && (editing || canAuthor) && (
                    <Button
                        emphasis='quaternary'
                        size='sm'
                        className={classNames('docs-btn-neutral', styles.iconLabel)}
                        leadingIcon={editing ? <CheckIcon size={18}/> : <PencilOutlineIcon size={18}/>}

                        // The control is a mode toggle, and a changed label on a
                        // button that already holds focus is not reliably
                        // re-announced.
                        aria-pressed={editing}
                        aria-keyshortcuts={editing ? undefined : 'e'}
                        onClick={onToggleEdit}
                    >
                        {editing ? (
                            <FormattedMessage
                                id='docs.space.close'
                                defaultMessage='Close'
                            />
                        ) : (
                            <FormattedMessage
                                id='docs.space.edit'
                                defaultMessage='Edit'
                            />
                        )}
                    </Button>
                )}

                {subject && (
                    <>
                        <PageMenu
                            spaceId={space.id}
                            pageId={subject.id}
                            pageTitle={subject.title}
                            isDraft={unpublished}
                            align='right'
                            tooltip={moreLabel}
                            trigger={(
                                <Button
                                    emphasis='quaternary'
                                    size='sm'
                                    className='btn-icon docs-btn-neutral'
                                    aria-label={moreLabel}
                                    leadingIcon={<DotsHorizontalIcon size={18}/>}
                                />
                            )}
                        />
                        <Button
                            emphasis='quaternary'
                            size='sm'
                            className={classNames('btn-icon', 'docs-btn-neutral', {active: fullscreen})}
                            tooltip={expandLabel}
                            aria-pressed={fullscreen}
                            leadingIcon={fullscreen ? <ArrowCollapseIcon size={18}/> : <ArrowExpandIcon size={18}/>}
                            onClick={toggleFullscreen}
                        />
                    </>
                )}
            </div>
        </div>
    );
};

export default PageHeader;

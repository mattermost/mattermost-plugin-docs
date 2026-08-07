// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {deletePageDraft, publishPageDraft, PublishConflictError} from 'client/drafts';
import {RestError} from 'client/rest';
import {useCaretAnchoredSuggestions} from 'hooks/caret_anchored_suggestions';
import {useDraftAutosave} from 'hooks/draft_autosave';
import {useDocsNavigation} from 'hooks/navigation';
import {usePageDraft} from 'hooks/page_draft';
import {usePagePresence} from 'hooks/page_presence';
import {usePinnedToolbar} from 'hooks/pinned_toolbar';
import {useCurrentUserId} from 'hooks/user';
import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {hostCanUseEditor, hostGetEditor, hostSupportsDocumentEditor} from 'webapp_globals';
import type {PublishedFormattingBarHandle, PublishedMarkdownMode, PublishedWysiwygEditorHandle} from 'webapp_globals';

import {PrimaryButton, SecondaryButton} from 'components/form_controls/button';

import type {Page} from 'types/docs';

import {applyWysiwygFormatting} from './apply_formatting';
import AutosaveIndicator from './autosave_indicator';
import {DOCS_EXTENSIONS} from './docs_extensions';
import ExitEditorDialog from './exit_editor_dialog';
import FloatingFormattingBar from './floating_formatting_bar';
import styles from './page_editor.module.scss';
import PublishConflictDialog from './publish_conflict_dialog';
import {CalloutControl, PinToolbarControl} from './toolbar_controls';

type Props = {
    spaceId: string;
    pageId: string;
    isDraft: boolean;
};

type Conflict = {
    reason: string;
    currentPage: Page | null;
    exitAfter: boolean;
};

const PageEditor = ({spaceId, pageId, isDraft}: Props) => {
    const {formatMessage} = useIntl();
    const currentUserId = useCurrentUserId();
    const {goToPage} = useDocsNavigation();
    const activeEditors = usePagePresence(spaceId, pageId, currentUserId);
    const load = usePageDraft(spaceId, pageId);

    const editorRef = useRef<PublishedWysiwygEditorHandle | null>(null);
    const formattingBarRef = useRef<PublishedFormattingBarHandle | null>(null);
    const editorSurfaceRef = useRef<HTMLDivElement>(null);

    const getHostEditor = useCallback(() => editorRef.current?.getEditor?.() ?? null, []);

    const [pinned, togglePinned] = usePinnedToolbar();
    useCaretAnchoredSuggestions(editorSurfaceRef, !load.loading && !load.error);
    const [documentMode, setDocumentMode] = useState<boolean | null>(null);
    const [showExitDialog, setShowExitDialog] = useState(false);
    const [conflict, setConflict] = useState<Conflict | null>(null);
    const [actionError, setActionError] = useState<unknown>(null);
    const [contentError, setContentError] = useState(false);
    const [busy, setBusy] = useState(false);

    const busyRef = useRef(false);
    const [baseEditAt, setBaseEditAt] = useState<number | undefined>(undefined);
    const [draftExists, setDraftExists] = useState(false);

    const onDraftSaved = useCallback(() => {
        setDraftExists(true);
    }, []);

    const autosave = useDraftAutosave({
        spaceId,
        pageId,
        enabled: !load.loading && !load.error && !contentError,
        baseEditAt,
        onSaved: onDraftSaved,
        onError: setActionError,
    });

    useEffect(() => {
        setBaseEditAt(load.baseEditAt);
    }, [load.baseEditAt]);

    useEffect(() => {
        setContentError(false);
        setDraftExists(false);
        setActionError(null);

        setConflict(null);
        setShowExitDialog(false);
    }, [spaceId, pageId]);

    useEffect(() => {
        if (load.loading) {
            return;
        }
        setDocumentMode(hostSupportsDocumentEditor(editorRef.current));
    }, [load.loading]);

    const onContentChange = useCallback((content: string) => {
        if (contentError || editorRef.current?.hasContentError?.()) {
            setContentError(true);
            return;
        }
        autosave.queue({body: content});
    }, [autosave, contentError]);

    const onContentError = useCallback(() => {
        setContentError(true);
    }, []);

    const applyFormatting = useCallback((mode: PublishedMarkdownMode) => {
        const editor = editorRef.current?.getEditor?.() as Parameters<typeof applyWysiwygFormatting>[0] | undefined;
        if (!editor || editor.isDestroyed) {
            return;
        }
        if (mode === 'link') {
            formattingBarRef.current?.openLinkPopover();
            return;
        }
        applyWysiwygFormatting(editor, mode);
    }, []);

    const toolbarControls = useMemo(() => [
        <CalloutControl
            key='callout'
            getEditor={getHostEditor}
        />,
        <PinToolbarControl
            key='pin'
            pinned={pinned}
            onToggle={togglePinned}
        />,
    ], [getHostEditor, pinned, togglePinned]);

    const leave = useCallback(() => goToPage(spaceId, pageId), [goToPage, spaceId, pageId]);

    const publish = useCallback(async (force: boolean, exitAfter = false) => {
        if (busyRef.current) {
            return;
        }
        busyRef.current = true;
        setBusy(true);
        setActionError(null);
        try {
            if (!await autosave.flush()) {
                return;
            }

            const published = await publishPageDraft(spaceId, pageId, force);
            setBaseEditAt(published.edit_at);
            setConflict(null);
            setShowExitDialog(false);
            if (exitAfter) {
                leave();
            }
        } catch (error) {
            if (error instanceof PublishConflictError) {
                setShowExitDialog(false);
                setConflict({reason: error.reason, currentPage: error.currentPage, exitAfter});
                return;
            }
            setActionError(error);
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    }, [autosave, spaceId, pageId, leave]);

    const discard = useCallback(async () => {
        if (busyRef.current) {
            return;
        }
        busyRef.current = true;
        setBusy(true);
        setActionError(null);
        autosave.cancel();
        try {
            await deletePageDraft(spaceId, pageId);
            setShowExitDialog(false);
            leave();
        } catch (error) {
            // The server answers 404 when there is no draft left to remove, which is
            // the outcome discard asked for.
            if (error instanceof RestError && error.status === 404) {
                setShowExitDialog(false);
                leave();
            } else {
                setActionError(error);
            }
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    }, [autosave, spaceId, pageId, leave]);

    const saveDraftAndLeave = useCallback(async () => {
        if (busyRef.current) {
            return;
        }
        busyRef.current = true;
        setBusy(true);
        setActionError(null);
        try {
            if (!await autosave.flush()) {
                return;
            }
            setShowExitDialog(false);
            leave();
        } finally {
            busyRef.current = false;
            setBusy(false);
        }
    }, [autosave, leave]);

    const onPublish = useCallback(() => {
        publish(false);
    }, [publish]);

    const onClose = useCallback(() => {
        if (autosave.status === 'saved' && !load.fromDraft && !draftExists) {
            leave();
            return;
        }
        setShowExitDialog(true);
    }, [autosave.status, load.fromDraft, draftExists, leave]);

    if (!hostCanUseEditor()) {
        return (
            <div className={styles.empty}>
                <FormattedMessage
                    id='docs.editor.hostMissing'
                    defaultMessage='This Mattermost build does not publish the Docs editor. Update the server to edit pages here.'
                />
            </div>
        );
    }

    if (load.loading) {
        return (
            <div className={styles.empty}>
                <FormattedMessage
                    id='docs.editor.loading'
                    defaultMessage='Loading page…'
                />
            </div>
        );
    }

    if (load.error) {
        return (
            <div className={styles.empty}>
                <FormattedMessage
                    id='docs.editor.loadFailed'
                    defaultMessage='This page could not be loaded. Refresh to try again.'
                />
            </div>
        );
    }

    if (load.notFound) {
        return (
            <div className={styles.empty}>
                <FormattedMessage
                    id='docs.editor.notFound'
                    defaultMessage='This page does not exist, or you do not have access to it.'
                />
            </div>
        );
    }

    const {WysiwygEditor, FormattingBar} = hostGetEditor() ?? {};
    if (!WysiwygEditor) {
        return null;
    }

    return (
        <div className={styles.root}>
            <div className={styles.header}>
                {activeEditors.length > 0 && (
                    <span className={styles.presence}>
                        <FormattedMessage
                            id='docs.editor.presence'
                            defaultMessage='{count, plural, one {# other editor} other {# other editors}}'
                            values={{count: activeEditors.length}}
                        />
                    </span>
                )}
                <AutosaveIndicator status={autosave.status}/>
                <PrimaryButton
                    onClick={onPublish}
                    disabled={busy || contentError}
                >
                    {load.page ? (
                        <FormattedMessage
                            id='docs.editor.update'
                            defaultMessage='Update'
                        />
                    ) : (
                        <FormattedMessage
                            id='docs.editor.publish'
                            defaultMessage='Publish'
                        />
                    )}
                </PrimaryButton>
                <SecondaryButton onClick={onClose}>
                    <FormattedMessage
                        id='docs.editor.close'
                        defaultMessage='Close'
                    />
                </SecondaryButton>
            </div>

            <div
                className={styles.scroll}
                data-docs-scroll=''
            >
                <div className={styles.column}>
                    {documentMode === false && (
                        <div className={styles.notice}>
                            <FormattedMessage
                                id='docs.editor.legacyHost'
                                defaultMessage='This server predates structured page content. Formatting beyond basic text may not be saved.'
                            />
                        </div>
                    )}

                    {contentError && (
                        <div
                            className={styles.error}
                            role='alert'
                        >
                            <FormattedMessage
                                id='docs.editor.contentError'
                                defaultMessage="This page's content could not be opened. Editing is disabled so the stored version is not overwritten."
                            />
                        </div>
                    )}

                    {actionError != null && (
                        <div
                            className={styles.error}
                            role='alert'
                        >
                            <FormattedMessage
                                id='docs.editor.actionFailed'
                                defaultMessage='That action could not be completed. Your draft is unchanged.'
                            />
                        </div>
                    )}

                    <div
                        ref={editorSurfaceRef}
                        className={styles.surface}
                    >
                        {pinned && FormattingBar ? (
                            <div className={styles.pinnedToolbar}>
                                <FormattingBar
                                    ref={formattingBarRef}
                                    applyFormatting={applyFormatting}
                                    disableControls={false}
                                    location='docs'
                                    getEditor={getHostEditor}
                                    additionalControls={toolbarControls}
                                />
                            </div>
                        ) : (
                            <FloatingFormattingBar
                                editorRef={editorSurfaceRef}
                                applyFormatting={applyFormatting}
                                getEditor={getHostEditor}
                                barRef={formattingBarRef}
                                additionalControls={toolbarControls}
                            />
                        )}

                        <WysiwygEditor
                            ref={editorRef}
                            value={load.body}
                            onChange={onContentChange}
                            onSubmit={onPublish}
                            useCtrlSend={true}
                            contentType='json'
                            extensions={DOCS_EXTENSIONS}
                            onContentError={onContentError}
                            disabled={contentError}
                            channelId=''
                            placeholder={formatMessage({id: 'docs.editor.bodyPlaceholder', defaultMessage: 'Start writing…'})}
                            id={isDraft ? 'docs-draft-editor' : 'docs-page-editor'}
                        />
                    </div>
                </div>
            </div>

            {showExitDialog && (
                <ExitEditorDialog
                    onPublish={() => {
                        publish(false, true);
                    }}
                    onSaveDraft={saveDraftAndLeave}
                    busy={busy}
                    failed={actionError != null}
                    onDiscard={() => {
                        discard();
                    }}
                    onClose={() => setShowExitDialog(false)}
                />
            )}

            {conflict && (
                <PublishConflictDialog
                    reason={conflict.reason}
                    currentPage={conflict.currentPage}
                    busy={busy}
                    failed={actionError != null}
                    onForcePublish={() => {
                        publish(true, conflict.exitAfter);
                    }}
                    onClose={() => setConflict(null)}
                />
            )}
        </div>
    );
};

export default PageEditor;

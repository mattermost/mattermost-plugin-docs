// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {usePublishDraft} from 'hooks/drafts';
import {useHostEditor} from 'hooks/host_editor';
import {usePageEditing} from 'hooks/page_editing';
import {usePinnedToolbar} from 'hooks/pinned_toolbar';
import React, {useCallback, useMemo, useRef} from 'react';
import {createPortal} from 'react-dom';
import {FormattedMessage, useIntl} from 'react-intl';
import {hostCanUseEditor, hostGetEditor} from 'webapp_globals';
import type {PublishedWysiwygEditorHandle} from 'webapp_globals';

import SectionNotice from 'components/section_notice/section_notice';

import {DOCS_EXTENSIONS} from './docs_extensions';
import FloatingFormattingBar from './floating_formatting_bar';
import styles from './page_editor.module.scss';
import {CalloutControl, OverflowControl, PinToolbarControl} from './toolbar_controls';
import {useToolbarSlot} from './toolbar_slot';

type Props = {
    spaceId: string;
    pageId: string;
    isDraft: boolean;
    editing: boolean;
};

const PageEditor = ({spaceId, pageId, isDraft, editing}: Props) => {
    const {formatMessage} = useIntl();
    const [pinned, togglePinned] = usePinnedToolbar();
    const toolbarSlot = useToolbarSlot();

    const editorRef = useRef<PublishedWysiwygEditorHandle | null>(null);

    const {load, contentError, actionError, onContentChange, onContentError} = usePageEditing({
        spaceId,
        pageId,
        editing,
        editorRef,
    });

    const {formattingBarRef, surfaceRef, getEditor, applyFormatting, documentMode} =
        useHostEditor(editorRef, editing && !load.loading && !load.error);

    const publish = usePublishDraft(spaceId);
    const onSubmit = useCallback(() => {
        publish(pageId);
    }, [publish, pageId]);

    const calloutControl = useMemo(() => (
        <CalloutControl
            key='callout'
            getEditor={getEditor}
        />
    ), [getEditor]);

    const floatingControls = useMemo(() => [
        calloutControl,
        <OverflowControl
            key='overflow'
            onPin={togglePinned}
        />,
    ], [calloutControl, togglePinned]);

    const pinnedControls = useMemo(() => [
        calloutControl,
        <PinToolbarControl
            key='pin'
            pinned={true}
            onToggle={togglePinned}
        />,
    ], [calloutControl, togglePinned]);

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
            {editing && pinned && FormattingBar && toolbarSlot && createPortal(
                <div className={styles.pinnedToolbar}>
                    <FormattingBar
                        ref={formattingBarRef}
                        applyFormatting={applyFormatting}
                        disableControls={false}
                        location='docs'
                        getEditor={getEditor}
                        additionalControls={pinnedControls}
                    />
                </div>,
                toolbarSlot,
            )}

            <div className={styles.scroll}>
                <div className={styles.column}>
                    {documentMode === false && (
                        <SectionNotice
                            className={styles.banner}
                            variant='warning'
                            title={(
                                <FormattedMessage
                                    id='docs.editor.legacyHost'
                                    defaultMessage='This server predates structured page content. Formatting beyond basic text may not be saved.'
                                />
                            )}
                        />
                    )}

                    {contentError && (
                        <SectionNotice
                            className={styles.banner}
                            variant='error'
                            role='alert'
                            title={(
                                <FormattedMessage
                                    id='docs.editor.contentError'
                                    defaultMessage="This page's content could not be opened. Editing is disabled so the stored version is not overwritten."
                                />
                            )}
                        />
                    )}

                    {actionError != null && (
                        <SectionNotice
                            className={styles.banner}
                            variant='error'
                            role='alert'
                            title={(
                                <FormattedMessage
                                    id='docs.editor.actionFailed'
                                    defaultMessage='That action could not be completed. Your draft is unchanged.'
                                />
                            )}
                        />
                    )}

                    <div
                        ref={surfaceRef}
                        className={classNames(styles.surface, {[styles.reading]: !editing})}
                    >
                        {editing && !pinned && (
                            <FloatingFormattingBar
                                editorRef={surfaceRef}
                                applyFormatting={applyFormatting}
                                getEditor={getEditor}
                                barRef={formattingBarRef}
                                additionalControls={floatingControls}
                            />
                        )}

                        <WysiwygEditor
                            ref={editorRef}
                            value={load.body}
                            onChange={onContentChange}
                            onSubmit={onSubmit}
                            useCtrlSend={true}
                            contentType='json'
                            extensions={DOCS_EXTENSIONS}
                            onContentError={onContentError}
                            readOnly={!editing && !contentError}
                            disabled={contentError || !editing}
                            channelId=''
                            placeholder={formatMessage({id: 'docs.editor.bodyPlaceholder', defaultMessage: 'Start writing…'})}
                            id={isDraft ? 'docs-draft-editor' : 'docs-page-editor'}
                        />
                    </div>
                </div>
            </div>
        </div>
    );
};

export default PageEditor;

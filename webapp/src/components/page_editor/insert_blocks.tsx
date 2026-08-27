// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Editor} from '@tiptap/core';
import React from 'react';
import {defineMessages} from 'react-intl';
import type {MessageDescriptor} from 'react-intl';

import {
    BullhornOutlineIcon,
    CodeTagsIcon,
    FormatHeader1Icon,
    FormatHeader2Icon,
    FormatHeader3Icon,
    FormatHeader4Icon,
    FormatLetterCaseIcon,
    FormatListBulletedIcon,
    FormatListNumberedIcon,
    FormatQuoteOpenIcon,
    MinusIcon,
    TablePlusIcon,
} from '@mattermost/compass-icons/components';

export type BlockRange = {from: number; to: number};

export type InsertBlock = {
    id: string;
    icon: React.ReactNode;
    title: MessageDescriptor;
    insert: (editor: Editor, range: BlockRange) => void;
};

const messages = defineMessages({
    paragraph: {id: 'docs.editor.insert.paragraph', defaultMessage: 'Normal text'},
    heading1: {id: 'docs.editor.insert.heading1', defaultMessage: 'Heading 1'},
    heading2: {id: 'docs.editor.insert.heading2', defaultMessage: 'Heading 2'},
    heading3: {id: 'docs.editor.insert.heading3', defaultMessage: 'Heading 3'},
    heading4: {id: 'docs.editor.insert.heading4', defaultMessage: 'Heading 4'},
    bulletList: {id: 'docs.editor.insert.bulletList', defaultMessage: 'Bulleted list'},
    orderedList: {id: 'docs.editor.insert.orderedList', defaultMessage: 'Numbered list'},
    quote: {id: 'docs.editor.insert.quote', defaultMessage: 'Quote'},
    callout: {id: 'docs.editor.insert.callout', defaultMessage: 'Callout'},
    codeBlock: {id: 'docs.editor.insert.codeBlock', defaultMessage: 'Code block'},
    divider: {id: 'docs.editor.insert.divider', defaultMessage: 'Divider'},
    table: {id: 'docs.editor.insert.table', defaultMessage: 'Table'},
});

const ICON_SIZE = 18;

// The host owns the extensions these commands come from, and the plugin doesn't depend on
// them, so the chain is typed here rather than imported.
type HostChain = {
    focus: () => HostChain;
    deleteRange: (range: BlockRange) => HostChain;
    setParagraph: () => HostChain;
    toggleHeading: (attributes: {level: number}) => HostChain;
    toggleBulletList: () => HostChain;
    toggleOrderedList: () => HostChain;
    toggleBlockquote: () => HostChain;
    toggleCallout: (type: string) => HostChain;
    toggleCodeBlock: () => HostChain;
    setHorizontalRule: () => HostChain;
    insertTable: (options: {rows: number; cols: number; withHeaderRow: boolean}) => HostChain;
    run: () => boolean;
};

const replacing = (editor: Editor, range: BlockRange): HostChain =>
    (editor.chain() as unknown as HostChain).focus().deleteRange(range);

const heading = (level: 1 | 2 | 3 | 4, icon: React.ReactNode, title: MessageDescriptor): InsertBlock => ({
    id: `heading${level}`,
    icon,
    title,
    insert: (editor, range) => replacing(editor, range).toggleHeading({level}).run(),
});

// Ordered as the design's Basic blocks section is, so the muscle memory built there
// carries over.
export const INSERT_BLOCKS: InsertBlock[] = [
    {
        id: 'paragraph',
        icon: <FormatLetterCaseIcon size={ICON_SIZE}/>,
        title: messages.paragraph,
        insert: (editor, range) => replacing(editor, range).setParagraph().run(),
    },
    heading(1, <FormatHeader1Icon size={ICON_SIZE}/>, messages.heading1),
    heading(2, <FormatHeader2Icon size={ICON_SIZE}/>, messages.heading2),
    heading(3, <FormatHeader3Icon size={ICON_SIZE}/>, messages.heading3),
    heading(4, <FormatHeader4Icon size={ICON_SIZE}/>, messages.heading4),
    {
        id: 'bulletList',
        icon: <FormatListBulletedIcon size={ICON_SIZE}/>,
        title: messages.bulletList,
        insert: (editor, range) => replacing(editor, range).toggleBulletList().run(),
    },
    {
        id: 'orderedList',
        icon: <FormatListNumberedIcon size={ICON_SIZE}/>,
        title: messages.orderedList,
        insert: (editor, range) => replacing(editor, range).toggleOrderedList().run(),
    },
    {
        id: 'quote',
        icon: <FormatQuoteOpenIcon size={ICON_SIZE}/>,
        title: messages.quote,
        insert: (editor, range) => replacing(editor, range).toggleBlockquote().run(),
    },
    {
        id: 'callout',
        icon: <BullhornOutlineIcon size={ICON_SIZE}/>,
        title: messages.callout,
        insert: (editor, range) => replacing(editor, range).toggleCallout('info').run(),
    },
    {
        id: 'codeBlock',
        icon: <CodeTagsIcon size={ICON_SIZE}/>,
        title: messages.codeBlock,
        insert: (editor, range) => replacing(editor, range).toggleCodeBlock().run(),
    },
    {
        id: 'divider',
        icon: <MinusIcon size={ICON_SIZE}/>,
        title: messages.divider,
        insert: (editor, range) => replacing(editor, range).setHorizontalRule().run(),
    },
    {
        id: 'table',
        icon: <TablePlusIcon size={ICON_SIZE}/>,
        title: messages.table,
        insert: (editor, range) => replacing(editor, range).insertTable({
            rows: 3,
            cols: 3,
            withHeaderRow: true,
        }).run(),
    },
];

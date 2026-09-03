// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Editor} from '@tiptap/core';

import {filterBlocks, findSlashMatch} from './slash_menu';

// The caret sits at the end of `text`, in a block that starts at `start`.
const editorWith = (text: string, {start = 1, empty = true} = {}) => ({
    state: {
        selection: {
            empty,
            from: start + text.length,
            $from: {start: () => start},
        },
        doc: {
            textBetween: () => text,
        },
    },
} as unknown as Editor);

describe('findSlashMatch', () => {
    test('opens on a trigger at the start of a block', () => {
        expect(findSlashMatch(editorWith('/'))).toEqual({from: 1, to: 2, query: ''});
    });

    test('carries what has been typed since the trigger', () => {
        expect(findSlashMatch(editorWith('/tab'))).toEqual({from: 1, to: 5, query: 'tab'});
    });

    test('opens mid-line, where the trigger follows a space', () => {
        expect(findSlashMatch(editorWith('see /tab'))).toEqual({from: 5, to: 9, query: 'tab'});
    });

    test('leaves a path alone, since nothing separates the trigger from the text', () => {
        expect(findSlashMatch(editorWith('src/components'))).toBeNull();
    });

    test('leaves a date alone', () => {
        expect(findSlashMatch(editorWith('12/3'))).toBeNull();
    });

    test('closes once a space follows the trigger, which is prose rather than a command', () => {
        expect(findSlashMatch(editorWith('/tab and'))).toBeNull();
    });

    test('stays shut without a trigger', () => {
        expect(findSlashMatch(editorWith('table'))).toBeNull();
    });

    test('stays shut while text is selected, where an insert has no single home', () => {
        expect(findSlashMatch(editorWith('/tab', {empty: false}))).toBeNull();
    });

    test('measures from the block, not the document', () => {
        expect(findSlashMatch(editorWith('/tab', {start: 42}))).toEqual({from: 42, to: 46, query: 'tab'});
    });
});

describe('filterBlocks', () => {
    // Standing in for the translated title: the babel formatjs plugin compiles
    // defaultMessage to an AST, so it can't be read as a string here.
    const titles: Record<string, string> = {
        paragraph: 'Normal text',
        heading1: 'Heading 1',
        heading2: 'Heading 2',
        heading3: 'Heading 3',
        heading4: 'Heading 4',
        bulletList: 'Bulleted list',
        orderedList: 'Numbered list',
        quote: 'Quote',
        callout: 'Callout',
        codeBlock: 'Code block',
        divider: 'Divider',
        table: 'Table',
    };

    const label = (block: {id: string}) => titles[block.id];

    test('offers everything before anything is typed', () => {
        expect(filterBlocks('', label).map((block) => block.id)).toEqual(Object.keys(titles));
    });

    test('narrows to what the query matches, ignoring case', () => {
        expect(filterBlocks('TAB', label).map((block) => block.id)).toEqual(['table']);
    });

    test('matches on any part of the name', () => {
        expect(filterBlocks('ivid', label).map((block) => block.id)).toEqual(['divider']);
    });

    test('keeps every heading level a query like "head" turns up', () => {
        expect(filterBlocks('head', label).map((block) => block.id)).
            toEqual(['heading1', 'heading2', 'heading3', 'heading4']);
    });

    test('comes back empty when nothing matches, which closes the menu', () => {
        expect(filterBlocks('zzz', label)).toHaveLength(0);
    });
});

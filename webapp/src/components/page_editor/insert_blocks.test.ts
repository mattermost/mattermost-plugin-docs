// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Editor} from '@tiptap/core';

import {INSERT_BLOCKS} from './insert_blocks';

const RANGE = {from: 4, to: 8};

// Records the chain a block builds, so a test can assert on the commands rather than on a
// rendered document.
const editorWhere = (...active: string[]) => {
    const calls: string[] = [];

    const chain: Record<string, unknown> = {};
    const link = (name: string) => (...args: unknown[]) => {
        calls.push(args.length ? `${name}(${JSON.stringify(args[0])})` : name);
        return chain;
    };

    for (const name of [
        'focus', 'deleteRange', 'setParagraph', 'setHeading', 'toggleBulletList',
        'toggleOrderedList', 'setBlockquote', 'setCallout', 'setCodeBlock',
        'setHorizontalRule', 'insertTable', 'run',
    ]) {
        chain[name] = link(name);
    }

    const editor = {
        chain: () => chain,
        isActive: (name: string) => active.includes(name),
    } as unknown as Editor;

    return {editor, calls};
};

const run = (id: string, ...active: string[]) => {
    const block = INSERT_BLOCKS.find((candidate) => candidate.id === id);
    if (!block) {
        throw new Error(`no block named ${id}`);
    }

    const {editor, calls} = editorWhere(...active);
    block.insert(editor, RANGE);
    return calls;
};

describe('INSERT_BLOCKS', () => {
    test('every block clears the text that opened the menu', () => {
        for (const block of INSERT_BLOCKS) {
            const {editor, calls} = editorWhere();
            block.insert(editor, RANGE);
            expect(calls).toContain('deleteRange({"from":4,"to":8})');
        }
    });

    test.each([
        ['heading1', 'setHeading({"level":1})'],
        ['heading4', 'setHeading({"level":4})'],
        ['paragraph', 'setParagraph'],
        ['quote', 'setBlockquote'],
        ['codeBlock', 'setCodeBlock'],
        ['callout', 'setCallout("info")'],
        ['divider', 'setHorizontalRule'],
    ])('%s applies its block type', (id, command) => {
        expect(run(id)).toContain(command);
    });

    test('a table always lands, since inserting one is never a no-op', () => {
        expect(run('table', 'table')).toContain('insertTable({"rows":3,"cols":3,"withHeaderRow":true})');
    });

    // Picking the type a block already has should leave the block where it is. Toggles
    // would strip it back to a paragraph, which is the opposite of what the menu promises.
    test.each([
        ['heading2', 'heading', 'setHeading({"level":2})'],
        ['quote', 'blockquote', 'setBlockquote'],
        ['codeBlock', 'codeBlock', 'setCodeBlock'],
        ['callout', 'callout', 'setCallout("info")'],
    ])('%s stays put when the block is already one', (id, node, command) => {
        expect(run(id, node)).toContain(command);
    });

    test.each([
        ['bulletList', 'toggleBulletList'],
        ['orderedList', 'toggleOrderedList'],
    ])('%s only toggles when the block is not already a list', (id, command) => {
        expect(run(id)).toContain(command);
        expect(run(id, id)).not.toContain(command);
    });

    test('a list that is left alone still loses the trigger text', () => {
        expect(run('bulletList', 'bulletList')).toEqual([
            'focus',
            'deleteRange({"from":4,"to":8})',
            'run',
        ]);
    });
});

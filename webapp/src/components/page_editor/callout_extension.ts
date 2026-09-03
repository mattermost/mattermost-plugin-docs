// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Node, mergeAttributes} from '@tiptap/core';

export const CALLOUT_TYPES = ['info', 'note', 'success', 'warning', 'error'] as const;

export type CalloutType = (typeof CALLOUT_TYPES)[number];

const DEFAULT_TYPE: CalloutType = 'info';

declare module '@tiptap/core' {
    interface Commands<ReturnType> {
        callout: {
            setCallout: (type: CalloutType) => ReturnType;
            toggleCallout: (type: CalloutType) => ReturnType;
        };
    }
}

export const Callout = Node.create({
    name: 'callout',
    group: 'block',
    content: 'block+',
    defining: true,

    addAttributes() {
        return {
            type: {
                default: DEFAULT_TYPE,
                parseHTML: (element) => {
                    const value = element.getAttribute('data-callout-type');
                    return CALLOUT_TYPES.includes(value as CalloutType) ? value : DEFAULT_TYPE;
                },
                renderHTML: (attributes) => ({'data-callout-type': attributes.type}),
            },
        };
    },

    parseHTML() {
        return [{tag: 'div[data-callout-type]'}];
    },

    renderHTML({HTMLAttributes}) {
        return ['div', mergeAttributes(HTMLAttributes, {class: 'docs-callout'}), 0];
    },

    addCommands() {
        return {
            setCallout: (type: CalloutType) => ({editor, commands}) => {
                if (editor.isActive(this.name)) {
                    return commands.updateAttributes(this.name, {type});
                }
                return commands.wrapIn(this.name, {type});
            },

            toggleCallout: (type: CalloutType) => ({editor, commands}) => {
                if (editor.isActive(this.name) && !editor.isActive(this.name, {type})) {
                    return commands.updateAttributes(this.name, {type});
                }
                return commands.toggleWrap(this.name, {type});
            },
        };
    },
});

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {render, screen} from '@testing-library/react';
import React, {useRef} from 'react';

import FloatingFormattingBar from './floating_formatting_bar';

jest.mock('webapp_globals', () => ({
    hostGetEditor: () => ({
        FormattingBar: () => <div data-testid='formatting-bar'/>,
    }),
}));

const SELECTION_RECT = {
    width: 120,
    height: 20,
    top: 10,
    left: 10,
    right: 130,
    bottom: 30,
    x: 10,
    y: 10,
    toJSON: () => ({}),
} as DOMRect;

beforeAll(() => {
    Range.prototype.getBoundingClientRect = () => SELECTION_RECT;
    Range.prototype.getClientRects = () => [SELECTION_RECT] as unknown as DOMRectList;
});

const Harness = ({showBar}: {showBar: boolean}) => {
    const surfaceRef = useRef<HTMLDivElement | null>(null);

    return (
        <div ref={surfaceRef}>
            <p>{'page body text'}</p>
            {showBar && (
                <FloatingFormattingBar
                    editorRef={surfaceRef}
                    applyFormatting={jest.fn()}
                    getEditor={jest.fn()}
                    barRef={jest.fn()}
                />
            )}
        </div>
    );
};

const selectBody = () => {
    const range = document.createRange();
    range.selectNodeContents(screen.getByText('page body text'));

    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
};

const isHidden = (container: HTMLElement) => container.querySelector('.bar')!.className.includes('hidden');

describe('FloatingFormattingBar', () => {
    it('shows itself against a selection that was made before it mounted', () => {
        const {container, rerender} = render(<Harness showBar={false}/>);

        selectBody();
        rerender(<Harness showBar={true}/>);

        expect(isHidden(container)).toBe(false);
    });

    it('stays hidden when nothing is selected', () => {
        const {container} = render(<Harness showBar={true}/>);

        expect(isHidden(container)).toBe(true);
    });
});

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import ResizableDivider from './resizable_divider';

describe('ResizableDivider', () => {
    it('applies and commits the final pointer position without a pointer move', () => {
        const onResize = jest.fn();
        const onResizeEnd = jest.fn();
        render(
            <ResizableDivider
                ariaLabel='Resize panel'
                side='left'
                width={300}
                minWidth={200}
                maxWidth={500}
                defaultWidth={300}
                onResize={onResize}
                onResizeEnd={onResizeEnd}
            />,
        );

        const divider = screen.getByRole('separator', {name: 'Resize panel'});
        divider.setPointerCapture = jest.fn();
        divider.hasPointerCapture = jest.fn(() => true);
        divider.releasePointerCapture = jest.fn();

        fireEvent(divider, new MouseEvent('pointerdown', {bubbles: true, button: 0, clientX: 100}));
        fireEvent(divider, new MouseEvent('pointerup', {bubbles: true, clientX: 132}));

        expect(onResize).toHaveBeenLastCalledWith(332);
        expect(onResizeEnd).toHaveBeenCalledWith(332);
    });

    it('commits the last moved width when pointer capture is cancelled', () => {
        const onResize = jest.fn();
        const onResizeEnd = jest.fn();
        render(
            <ResizableDivider
                ariaLabel='Resize panel'
                side='left'
                width={300}
                minWidth={200}
                maxWidth={500}
                defaultWidth={300}
                onResize={onResize}
                onResizeEnd={onResizeEnd}
            />,
        );

        const divider = screen.getByRole('separator', {name: 'Resize panel'});
        divider.setPointerCapture = jest.fn();
        divider.hasPointerCapture = jest.fn(() => true);
        divider.releasePointerCapture = jest.fn();

        fireEvent(divider, new MouseEvent('pointerdown', {bubbles: true, button: 0, clientX: 100}));
        fireEvent(divider, new MouseEvent('pointermove', {bubbles: true, clientX: 132}));
        fireEvent(divider, new MouseEvent('pointercancel', {bubbles: true, clientX: 0}));

        expect(onResize).toHaveBeenCalledWith(332);
        expect(onResizeEnd).toHaveBeenCalledWith(332);
    });
});

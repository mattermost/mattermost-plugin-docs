// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import GenericModal, {useModalClose} from './generic_modal';

import {renderWithContext} from '../../../tests/react_testing_utils';

const baseProps = {
    title: 'My modal',
    onClose: jest.fn(),
};

const CloseContextProbe = () => <span>{useModalClose() ? 'Close available' : 'Close missing'}</span>;

describe('GenericModal', () => {
    it('renders the title, body, and footer', () => {
        renderWithContext(
            <GenericModal
                {...baseProps}
                footer={<button type='button'>{'Save'}</button>}
            >
                <p>{'Body content'}</p>
            </GenericModal>,
        );

        expect(screen.getByRole('heading', {name: 'My modal'})).toBeInTheDocument();
        expect(screen.getByText('Body content')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Save'})).toBeInTheDocument();
    });

    it('closes via the close button', () => {
        const onClose = jest.fn();
        renderWithContext(
            <GenericModal
                {...baseProps}
                onClose={onClose}
            >
                <p>{'Body'}</p>
            </GenericModal>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Close'}));
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('omits the close button when disabled', () => {
        renderWithContext(
            <GenericModal
                {...baseProps}
                showCloseButton={false}
            >
                <p>{'Body'}</p>
            </GenericModal>,
        );

        expect(screen.queryByRole('button', {name: 'Close'})).not.toBeInTheDocument();
    });

    it('stays open when closing is disabled', () => {
        const onClose = jest.fn();
        renderWithContext(
            <GenericModal
                {...baseProps}
                onClose={onClose}
                closeDisabled={true}
            >
                <p>{'Body'}</p>
            </GenericModal>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Close'}));

        expect(onClose).not.toHaveBeenCalled();
        expect(screen.getByRole('heading', {name: 'My modal'})).toBeInTheDocument();
    });

    it('provides modal close behavior to title actions', () => {
        renderWithContext(
            <GenericModal
                {...baseProps}
                titleActions={<CloseContextProbe/>}
            >
                <p>{'Body'}</p>
            </GenericModal>,
        );

        expect(screen.getByText('Close available')).toBeInTheDocument();
    });

    // A modal rendered inside another's JSX stacks through Base UI's nesting
    // rather than through the modal stack. Base UI renders no backdrop for a
    // nested dialog unless forced, and marks the outer popup instead — so both
    // halves are pinned here.
    describe('a modal opened from inside another', () => {
        const renderNested = () => renderWithContext(
            <GenericModal
                {...baseProps}
                title='Outer'
            >
                <GenericModal
                    {...baseProps}
                    title='Inner'
                >
                    <p>{'Inner body'}</p>
                </GenericModal>
            </GenericModal>,
        );

        // Base UI hides a covered dialog from the accessibility tree, so the outer
        // title is present in the DOM but has no `heading` role to query.
        it('renders both dialogs', () => {
            renderNested();

            expect(screen.getByText('Outer')).toBeInTheDocument();
            expect(screen.getByText('Inner')).toBeInTheDocument();
            expect(screen.getByRole('heading', {name: 'Inner'})).toBeInTheDocument();
        });

        it('marks the outer popup as covered, so it can recede', () => {
            renderNested();

            const covered = document.querySelectorAll('[data-nested-dialog-open]');

            // Only the outer popup is marked, and it holds only its own content —
            // the inner dialog is portaled to the body rather than nested in the
            // outer popup's DOM, even though it nests in the React tree.
            expect(covered).toHaveLength(1);
            expect(covered[0].textContent).toContain('Outer');
            expect(covered[0].textContent).not.toContain('Inner body');
        });

        it('paints each dialog in its own band, innermost highest', () => {
            renderNested();

            // Both the backdrop and the viewport of each dialog carry the level, in
            // portal order: outer pair first, then inner.
            const levels = [...document.querySelectorAll('[style*="--docs-modal-level"]')].
                map((el) => (el as HTMLElement).style.getPropertyValue('--docs-modal-level'));

            expect(levels).toEqual(['0', '0', '1', '1']);
        });
    });
});

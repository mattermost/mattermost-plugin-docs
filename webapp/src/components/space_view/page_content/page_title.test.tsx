// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import PageTitle from './page_title';

import {renderWithContext} from '../../../../tests/react_testing_utils';

const noop = () => {};

const renderTitle = (props: Partial<React.ComponentProps<typeof PageTitle>> = {}) =>
    renderWithContext(
        <PageTitle
            value='Runbook'
            editing={false}
            onChange={noop}
            onCommit={noop}
            onCancel={noop}
            {...props}
        />,
    );

describe('PageTitle view mode', () => {
    it('renders a heading and no editable field', () => {
        renderTitle();

        expect(screen.getByRole('heading', {name: 'Runbook'})).toBeInTheDocument();
        expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    });

    it('shows the untitled fallback when the page has no title', () => {
        renderTitle({value: ''});

        expect(screen.getByRole('heading', {name: 'Untitled'})).toBeInTheDocument();
    });
});

describe('PageTitle edit mode', () => {
    it('renders an editable field and no heading', () => {
        renderTitle({editing: true});

        expect(screen.getByRole('textbox', {name: 'Page title'})).toHaveValue('Runbook');
        expect(screen.queryByRole('heading')).not.toBeInTheDocument();
    });

    it('takes focus, since the field appears in response to asking to edit', () => {
        renderTitle({editing: true});

        expect(screen.getByRole('textbox', {name: 'Page title'})).toHaveFocus();
    });

    it('caps the field at the server title limit', () => {
        renderTitle({editing: true});

        expect(screen.getByRole('textbox', {name: 'Page title'})).toHaveAttribute('maxlength', '255');
    });

    it('reports each keystroke without committing', () => {
        const onChange = jest.fn();
        const onCommit = jest.fn();
        renderTitle({editing: true, onChange, onCommit});

        fireEvent.change(screen.getByRole('textbox', {name: 'Page title'}), {target: {value: 'Runbooks'}});

        expect(onChange).toHaveBeenCalledWith('Runbooks');
        expect(onCommit).not.toHaveBeenCalled();
    });

    it('commits on Enter without inserting a newline', () => {
        const onCommit = jest.fn();
        renderTitle({editing: true, onCommit});
        const field = screen.getByRole('textbox', {name: 'Page title'});

        const notPrevented = fireEvent.keyDown(field, {key: 'Enter'});

        expect(onCommit).toHaveBeenCalledTimes(1);
        expect(notPrevented).toBe(false);
    });

    it('commits on blur', () => {
        const onCommit = jest.fn();
        renderTitle({editing: true, onCommit});

        fireEvent.blur(screen.getByRole('textbox', {name: 'Page title'}));

        expect(onCommit).toHaveBeenCalledTimes(1);
    });

    it('cancels on Escape without committing', () => {
        const onCommit = jest.fn();
        const onCancel = jest.fn();
        renderTitle({editing: true, onCommit, onCancel});

        fireEvent.keyDown(screen.getByRole('textbox', {name: 'Page title'}), {key: 'Escape'});

        expect(onCancel).toHaveBeenCalledTimes(1);
        expect(onCommit).not.toHaveBeenCalled();
    });
});

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen} from '@testing-library/react';
import React from 'react';

import UrlInput from './url_input';
import type {UrlInputHandle} from './url_input';

import {renderWithContext} from '../../../tests/react_testing_utils';

const baseProps = {
    id: 'url',
    baseUrl: 'https://example.com/myteam/spaces',
    value: 'my-space',
    onChange: jest.fn(),
};

const EDIT_LABEL = 'Space URL';

describe('UrlInput', () => {
    it('shows a read-only preview with an Edit affordance by default', () => {
        renderWithContext(<UrlInput {...baseProps}/>);

        expect(screen.getByText(/https:\/\/example\.com\/myteam\/spaces\/my-space/)).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Edit'})).toBeInTheDocument();
        expect(screen.queryByLabelText(EDIT_LABEL)).not.toBeInTheDocument();
    });

    it('reveals and focuses the input when Edit is clicked', () => {
        renderWithContext(<UrlInput {...baseProps}/>);

        fireEvent.click(screen.getByRole('button', {name: 'Edit'}));

        const input = screen.getByLabelText(EDIT_LABEL);
        expect(input).toHaveValue('my-space');
        expect(input).toHaveFocus();
    });

    it('reports changes as the raw string value', () => {
        const onChange = jest.fn();
        renderWithContext(
            <UrlInput
                {...baseProps}
                onChange={onChange}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Edit'}));
        fireEvent.change(screen.getByLabelText(EDIT_LABEL), {target: {value: 'renamed'}});
        expect(onChange).toHaveBeenCalledWith('renamed');
    });

    it('commits and returns to preview on blur and on Enter', () => {
        const onBlur = jest.fn();
        renderWithContext(
            <UrlInput
                {...baseProps}
                onBlur={onBlur}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Edit'}));
        fireEvent.keyDown(screen.getByLabelText(EDIT_LABEL), {key: 'Enter'});

        expect(onBlur).toHaveBeenCalledTimes(1);
        expect(screen.queryByLabelText(EDIT_LABEL)).not.toBeInTheDocument();
    });

    it('renders an error message', () => {
        renderWithContext(
            <UrlInput
                {...baseProps}
                error='That URL is already taken'
            />,
        );
        expect(screen.getByText('That URL is already taken')).toBeInTheDocument();
    });

    it('enters edit mode and focuses the field via the imperative focus() handle', () => {
        const ref = React.createRef<UrlInputHandle>();
        renderWithContext(
            <UrlInput
                {...baseProps}
                ref={ref}
            />,
        );

        expect(screen.queryByLabelText(EDIT_LABEL)).not.toBeInTheDocument();

        act(() => ref.current!.focus());

        expect(screen.getByLabelText(EDIT_LABEL)).toHaveFocus();
    });
});

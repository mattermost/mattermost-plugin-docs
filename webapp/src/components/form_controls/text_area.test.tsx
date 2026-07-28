// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import TextArea from './text_area';

const baseProps = {
    id: 'desc',
    label: 'Description',
    value: '',
    onChange: jest.fn(),
};

describe('TextArea', () => {
    it('renders the value and uses the label as the accessible name', () => {
        render(
            <TextArea
                {...baseProps}
                value='notes'
            />,
        );
        expect(screen.getByLabelText('Description')).toHaveValue('notes');
    });

    it('reports changes as the raw string value', () => {
        const onChange = jest.fn();
        render(
            <TextArea
                {...baseProps}
                onChange={onChange}
            />,
        );

        fireEvent.change(screen.getByLabelText('Description'), {target: {value: 'typed'}});
        expect(onChange).toHaveBeenCalledWith('typed');
    });

    it('wires aria attributes and renders the error when invalid', () => {
        render(
            <TextArea
                {...baseProps}
                error='Too long'
            />,
        );

        const field = screen.getByLabelText('Description');
        expect(field).toHaveAttribute('aria-invalid', 'true');
        expect(field).toHaveAttribute('aria-describedby', 'desc-error');
        expect(screen.getByText('Too long')).toBeInTheDocument();
    });

    it('applies rows and maxLength', () => {
        render(
            <TextArea
                {...baseProps}
                rows={5}
                maxLength={100}
            />,
        );

        const field = screen.getByLabelText('Description');
        expect(field).toHaveAttribute('rows', '5');
        expect(field).toHaveAttribute('maxLength', '100');
    });
});

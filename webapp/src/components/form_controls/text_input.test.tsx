// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import TextInput from './text_input';

const baseProps = {
    id: 'field',
    label: 'Name',
    value: '',
    onChange: jest.fn(),
};

describe('TextInput', () => {
    it('renders the label and value', () => {
        render(
            <TextInput
                {...baseProps}
                value='hello'
            />,
        );

        const input = screen.getByLabelText('Name');
        expect(input).toHaveValue('hello');
    });

    it('reports changes as the raw string value', () => {
        const onChange = jest.fn();
        render(
            <TextInput
                {...baseProps}
                onChange={onChange}
            />,
        );

        fireEvent.change(screen.getByLabelText('Name'), {target: {value: 'typed'}});
        expect(onChange).toHaveBeenCalledWith('typed');
    });

    it('fires onEnter when Enter is pressed', () => {
        const onEnter = jest.fn();
        render(
            <TextInput
                {...baseProps}
                onEnter={onEnter}
            />,
        );

        fireEvent.keyDown(screen.getByLabelText('Name'), {key: 'Enter'});
        expect(onEnter).toHaveBeenCalledTimes(1);
    });

    it.each([
        {isComposing: true},
        {keyCode: 229},
    ])('ignores Enter while IME composition is active', (nativeEvent) => {
        const onEnter = jest.fn();
        render(
            <TextInput
                {...baseProps}
                onEnter={onEnter}
            />,
        );

        fireEvent.keyDown(screen.getByLabelText('Name'), {key: 'Enter', ...nativeEvent});
        expect(onEnter).not.toHaveBeenCalled();
    });

    it('exposes the error and wires aria attributes when invalid', () => {
        render(
            <TextInput
                {...baseProps}
                error='Required'
            />,
        );

        const input = screen.getByLabelText('Name');
        expect(input).toHaveAttribute('aria-invalid', 'true');
        expect(input).toHaveAttribute('aria-describedby', 'field-error');
        expect(screen.getByText('Required')).toHaveAttribute('id', 'field-error');
    });

    it('applies maxLength', () => {
        render(
            <TextInput
                {...baseProps}
                maxLength={10}
            />,
        );
        expect(screen.getByLabelText('Name')).toHaveAttribute('maxLength', '10');
    });
});

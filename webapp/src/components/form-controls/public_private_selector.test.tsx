// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import PublicPrivateSelector from './public_private_selector';
import type {SelectorOption} from './public_private_selector';

const options: SelectorOption[] = [
    {value: 'public', icon: <span/>, title: 'Public', description: 'Anyone'},
    {value: 'private', icon: <span/>, title: 'Private', description: 'Invited', disabled: true, disabledReason: 'Coming soon'},
];

const baseProps = {
    ariaLabel: 'Visibility',
    options,
    value: 'public',
    onChange: jest.fn(),
};

describe('PublicPrivateSelector', () => {
    it('renders options as a radiogroup and reflects the selected value', () => {
        render(<PublicPrivateSelector {...baseProps}/>);

        expect(screen.getByRole('radiogroup', {name: 'Visibility'})).toBeInTheDocument();
        expect(screen.getByRole('radio', {name: /Public/})).toHaveAttribute('aria-checked', 'true');
        expect(screen.getByRole('radio', {name: /Private/})).toHaveAttribute('aria-checked', 'false');
    });

    it('selects an enabled option on click', () => {
        const onChange = jest.fn();
        render(
            <PublicPrivateSelector
                {...baseProps}
                value='private'
                options={[{value: 'public', icon: <span/>, title: 'Public', description: 'Anyone'}, {value: 'internal', icon: <span/>, title: 'Internal', description: 'Team'}]}
                onChange={onChange}
            />,
        );

        fireEvent.click(screen.getByRole('radio', {name: /Public/}));
        expect(onChange).toHaveBeenCalledWith('public');
    });

    it('does not select a disabled option and surfaces its reason', () => {
        const onChange = jest.fn();
        render(
            <PublicPrivateSelector
                {...baseProps}
                onChange={onChange}
            />,
        );

        const disabled = screen.getByRole('radio', {name: /Private/});
        expect(disabled).toBeDisabled();
        expect(disabled).toHaveAttribute('title', 'Coming soon');

        fireEvent.click(disabled);
        expect(onChange).not.toHaveBeenCalled();
    });
});

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import {PrimaryButton, TertiaryButton} from './button';

describe('form-control buttons', () => {
    it('renders children and forwards clicks', () => {
        const onClick = jest.fn();
        render(<PrimaryButton onClick={onClick}>{'Create'}</PrimaryButton>);

        const button = screen.getByRole('button', {name: 'Create'});
        fireEvent.click(button);
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    it('forwards a ref to the underlying button element', () => {
        const ref = React.createRef<HTMLButtonElement>();
        render(<TertiaryButton ref={ref}>{'Cancel'}</TertiaryButton>);

        expect(ref.current).toBeInstanceOf(HTMLButtonElement);
        expect(ref.current).toHaveTextContent('Cancel');
    });
});

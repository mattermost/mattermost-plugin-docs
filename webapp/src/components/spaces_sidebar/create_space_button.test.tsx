// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, screen} from '@testing-library/react';
import React from 'react';

import CreateSpaceButton from './create_space_button';

import {renderWithContext} from '../../../tests/react_testing_utils';

describe('CreateSpaceButton', () => {
    it('renders the label and fires onClick', () => {
        const onClick = jest.fn();
        renderWithContext(<CreateSpaceButton onClick={onClick}/>);

        fireEvent.click(screen.getByRole('button', {name: 'Create a space'}));
        expect(onClick).toHaveBeenCalledTimes(1);
    });
});

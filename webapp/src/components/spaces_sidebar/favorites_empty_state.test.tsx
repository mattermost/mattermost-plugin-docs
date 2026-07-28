// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {screen} from '@testing-library/react';
import React from 'react';

import FavoritesEmptyState from './favorites_empty_state';

import {renderWithContext} from '../../../tests/react_testing_utils';

describe('FavoritesEmptyState', () => {
    it('renders the drop-target hint', () => {
        renderWithContext(<FavoritesEmptyState/>);
        expect(screen.getByText(/Drag favorite items here/)).toBeInTheDocument();
    });
});

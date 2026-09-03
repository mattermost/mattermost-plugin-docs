// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {render, screen} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import {SpaceIcon} from './space_icon';

// The compass glyphs are anonymous <svg> elements distinguishable only by their path data, so they
// are stood in for by named markers here.
jest.mock('@mattermost/compass-icons/components/globe', () => () => <span>{'globe'}</span>);
jest.mock('@mattermost/compass-icons/components/lock-outline', () => () => <span>{'lock'}</span>);

// The glyph is the only thing telling a reader who can see a space, so it has to follow the
// server's view_access. It once keyed off a client-only `visibility` field that no API response
// populated, which drew a lock over every open space.
describe('SpaceIcon', () => {
    it('shows the globe for an open space', () => {
        render(
            <SpaceIcon
                space={{...makeSpace('space1', 'Open'), view_access: 'open'}}
                size={16}
            />,
        );

        expect(screen.getByText('globe')).toBeInTheDocument();
    });

    it('shows the lock for a private space', () => {
        render(
            <SpaceIcon
                space={makeSpace('space1', 'Private')}
                size={16}
            />,
        );

        expect(screen.getByText('lock')).toBeInTheDocument();
    });

    it('shows the lock when view_access is not known', () => {
        render(<SpaceIcon size={16}/>);

        expect(screen.getByText('lock')).toBeInTheDocument();
    });

    it('prefers the space emoji over either glyph', () => {
        render(
            <SpaceIcon
                space={{...makeSpace('space1', 'Open'), icon: '📗', view_access: 'open'}}
                size={16}
            />,
        );

        expect(screen.getByText('📗')).toBeInTheDocument();
        expect(screen.queryByText('globe')).not.toBeInTheDocument();
    });
});

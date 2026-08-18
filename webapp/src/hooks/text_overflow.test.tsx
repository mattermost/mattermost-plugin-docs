// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {render, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {useTextOverflow} from './text_overflow';

const Probe = ({text}: {text: string}) => {
    const [overflowing, ref] = useTextOverflow();
    return (
        <span
            ref={ref}
            data-testid='text'
            data-overflowing={overflowing}
        >
            {text}
        </span>
    );
};

describe('useTextOverflow', () => {
    it('rechecks overflow when text changes without a resize', async () => {
        const {rerender} = render(<Probe text='short'/>);
        const node = screen.getByTestId('text');
        Object.defineProperties(node, {
            clientWidth: {configurable: true, value: 50},
            scrollWidth: {configurable: true, get: () => (node.textContent?.length ?? 0) * 10},
        });

        rerender(<Probe text='a much longer label'/>);
        await waitFor(() => expect(node).toHaveAttribute('data-overflowing', 'true'));

        rerender(<Probe text='tiny'/>);
        await waitFor(() => expect(node).toHaveAttribute('data-overflowing', 'false'));
    });
});

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {fireEvent, render, screen} from '@testing-library/react';
import React from 'react';

import SidebarItem from './sidebar_item';

// The real tooltip opens on a rested pointer — a timer no synthetic event drives —
// and portals itself out of the tree. The stub records what it was asked to do.
jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({title, disabled, children}: {title: string; disabled?: boolean; children: React.ReactNode}) => (
        <span
            data-tooltip={title}
            data-tooltip-disabled={String(Boolean(disabled))}
        >
            {children}
        </span>
    ),
}));

import {renderWithContext} from '../../../tests/react_testing_utils';

describe('SidebarItem', () => {
    it('renders leading, label, and trailing content', () => {
        render(
            <SidebarItem
                leading={<span>{'📄'}</span>}
                label='My Space'
                trailing={<button type='button'>{'menu'}</button>}
            />,
        );

        expect(screen.getByText('📄')).toBeInTheDocument();
        expect(screen.getByText('My Space')).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'menu'})).toBeInTheDocument();
    });

    it('fires onClick', () => {
        const onClick = jest.fn();
        render(
            <SidebarItem
                leading={<span/>}
                label='Home'
                title='Home'
                onClick={onClick}
            />,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Home'}));
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    it('marks the active state on the container', () => {
        const {container} = render(
            <SidebarItem
                leading={<span/>}
                label='Active'
                active={true}
            />,
        );

        expect(container.firstChild).toHaveClass('active');
    });
});

// Rows that navigate are anchors, so they support new-tab, copy and middle-click;
// rows that act (a toggle, a menu) stay buttons.
describe('SidebarItem as a link', () => {
    it('renders an anchor when given a destination', () => {
        renderWithContext(
            <SidebarItem
                leading={null}
                label='Engineering'
                to='/myteam/spaces/eng'
            />,
        );

        expect(screen.getByRole('link', {name: 'Engineering'})).toHaveAttribute('href', '/myteam/spaces/eng');
        expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('stays a button without one', () => {
        renderWithContext(
            <SidebarItem
                leading={null}
                label='Engineering'
                onClick={jest.fn()}
            />,
        );

        expect(screen.getByRole('button', {name: 'Engineering'})).toBeInTheDocument();
    });
});

// jsdom has no layout, so a clipped label is faked at the property level: the hook
// behind the tooltip compares scrollWidth against clientWidth. The tooltip itself is
// stubbed — it opens on a rested pointer, which no synthetic event reproduces — so
// what is asserted here is the gate, which is the part this component owns.
describe('SidebarItem label tooltip', () => {
    const setLabelWidths = (scroll: number, client: number) => {
        jest.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockReturnValue(scroll);
        jest.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(client);
    };

    afterEach(() => jest.restoreAllMocks());

    const renderLabel = (label: string, widths: [number, number], title?: string) => {
        setLabelWidths(...widths);
        render(
            <SidebarItem
                leading={null}
                label={label}
                title={title}
            />,
        );
        return screen.getByText(label).closest('[data-tooltip]');
    };

    it('offers the full name for a clipped label', () => {
        expect(renderLabel('A space with a very long name', [240, 120], 'A space with a very long name')).
            toHaveAttribute('data-tooltip-disabled', 'false');
    });

    it('stays quiet when the label fits', () => {
        expect(renderLabel('Engineering', [120, 120], 'Engineering')).
            toHaveAttribute('data-tooltip-disabled', 'true');
    });

    it('renders no tooltip for a row with no name to fall back to', () => {
        expect(renderLabel('Home', [240, 120])).toBeNull();
    });
});

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Menu as BaseMenu} from '@base-ui-components/react/menu';
import classNames from 'classnames';
import React from 'react';

import {WithTooltip} from '@mattermost/shared/components/tooltip';

import type {MenuItemSpec} from './menu_types';
import './menu.scss';

type Props = {
    ariaLabel: string;
    items: MenuItemSpec[];
    align?: 'left' | 'right';

    // Optional tooltip shown on the trigger.
    tooltip?: string;

    // The trigger element (e.g. a styled button). Base UI merges its own
    // open/aria/ref props onto it.
    trigger: React.ReactElement;
};

// Accessible dropdown menu built on Base UI's headless Menu (roving focus,
// arrow-key navigation, typeahead, ARIA semantics) and styled with our SCSS.
// Portals to the body so it is never clipped by the sidebar's overflow.
const Menu = ({ariaLabel, items, align = 'left', tooltip, trigger}: Props) => (
    <BaseMenu.Root>
        {tooltip ? (
            <WithTooltip title={tooltip}>
                <BaseMenu.Trigger render={trigger}/>
            </WithTooltip>
        ) : (
            <BaseMenu.Trigger render={trigger}/>
        )}
        <BaseMenu.Portal>
            <BaseMenu.Positioner
                className='DocsMenu__positioner'
                side='bottom'
                align={align === 'right' ? 'end' : 'start'}
                sideOffset={4}
                collisionPadding={8}
            >
                <BaseMenu.Popup
                    className='DocsMenu__popover'
                    aria-label={ariaLabel}
                >
                    {items.map((item) => (
                        <React.Fragment key={item.id}>
                            {item.hasDivider && <BaseMenu.Separator className='DocsMenu__divider'/>}
                            <BaseMenu.Item
                                className={classNames('DocsMenu__item', {'DocsMenu__item--destructive': item.isDestructive})}
                                onClick={item.onClick}
                            >
                                {item.leadingIcon && <span className='DocsMenu__itemIcon'>{item.leadingIcon}</span>}
                                <span className='DocsMenu__itemLabel'>{item.label}</span>
                            </BaseMenu.Item>
                        </React.Fragment>
                    ))}
                </BaseMenu.Popup>
            </BaseMenu.Positioner>
        </BaseMenu.Portal>
    </BaseMenu.Root>
);

export default Menu;

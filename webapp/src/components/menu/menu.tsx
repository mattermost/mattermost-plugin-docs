// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Menu as BaseMenu} from '@base-ui-components/react/menu';
import classNames from 'classnames';
import React from 'react';

import {WithTooltip} from '@mattermost/shared/components/tooltip';

import styles from './menu.module.scss';
import type {MenuItemSpec} from './menu_types';

type Props = {
    ariaLabel: string;
    items: MenuItemSpec[];
    align?: 'left' | 'right';
    tooltip?: string;

    // Base UI merges its own open/aria/ref props onto the trigger element.
    trigger: React.ReactElement;
};

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
                className={styles.positioner}
                side='bottom'
                align={align === 'right' ? 'end' : 'start'}
                sideOffset={4}
                collisionPadding={8}
            >
                <BaseMenu.Popup
                    className={styles.popover}
                    aria-label={ariaLabel}
                >
                    {items.map((item) => (
                        <React.Fragment key={item.id}>
                            {item.hasDivider && <BaseMenu.Separator className={styles.divider}/>}
                            <BaseMenu.Item
                                className={classNames(styles.item, {
                                    [styles.destructive]: item.isDestructive,
                                    [styles.link]: item.isLink,
                                })}
                                onClick={item.onClick}
                            >
                                {item.leadingIcon && <span className={styles.itemIcon}>{item.leadingIcon}</span>}
                                <span className={styles.itemText}>
                                    <span className={styles.itemLabel}>{item.label}</span>
                                    {item.secondaryLabel && <span className={styles.itemSecondary}>{item.secondaryLabel}</span>}
                                </span>
                            </BaseMenu.Item>
                        </React.Fragment>
                    ))}
                </BaseMenu.Popup>
            </BaseMenu.Positioner>
        </BaseMenu.Portal>
    </BaseMenu.Root>
);

export default Menu;

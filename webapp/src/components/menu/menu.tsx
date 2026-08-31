// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Menu as BaseMenu} from '@base-ui-components/react/menu';
import classNames from 'classnames';
import React from 'react';

import CheckIcon from '@mattermost/compass-icons/components/check';
import ChevronRightIcon from '@mattermost/compass-icons/components/chevron-right';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import styles from './menu.module.scss';

type MenuProps = {
    ariaLabel: string;
    align?: 'left' | 'right';
    tooltip?: string;

    // Base UI merges its own open/aria/ref props onto the trigger element.
    trigger: React.ReactElement;

    // Controlled open state. Only needed when something other than the trigger
    // opens the menu (e.g. a keyboard shortcut on the surrounding row).
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    children: React.ReactNode;
};

type ItemProps = {
    leadingIcon?: React.ReactNode;
    trailingIcon?: React.ReactNode;
    secondaryLabel?: React.ReactNode;
    destructive?: boolean;
    disabled?: boolean;
    closeOnClick?: boolean;
    onClick?: () => void;
    children: React.ReactNode;
};

type LinkItemProps = ItemProps & {
    href: string;
    external?: boolean;
};

type CheckboxItemProps = Pick<ItemProps, 'secondaryLabel' | 'disabled' | 'children'> & {
    checked: boolean;
    onCheckedChange: (checked: boolean) => void;
};

type RadioGroupProps = {
    value?: string;
    onValueChange: (value: string) => void;
    children: React.ReactNode;
};

type RadioItemProps = Pick<ItemProps, 'leadingIcon' | 'secondaryLabel' | 'disabled' | 'children'> & {
    value: string;
};

type SubmenuProps = {
    label: React.ReactNode;
    leadingIcon?: React.ReactNode;
    ariaLabel?: string;
    disabled?: boolean;
    children: React.ReactNode;
};

const ItemBody = ({leadingIcon, trailingIcon, secondaryLabel, children}: Pick<ItemProps, 'leadingIcon' | 'trailingIcon' | 'secondaryLabel' | 'children'>) => (
    <>
        {leadingIcon && (
            <span
                className={styles.itemIcon}
                aria-hidden={true}
            >
                {leadingIcon}
            </span>
        )}
        <span className={styles.itemText}>
            <span className={styles.itemLabel}>{children}</span>
            {secondaryLabel && <span className={styles.itemSecondary}>{secondaryLabel}</span>}
        </span>
        {trailingIcon && (
            <span
                className={styles.itemTrailing}
                aria-hidden={true}
            >
                {trailingIcon}
            </span>
        )}
    </>
);

/**
 * An interactive menu item. Renders as a button-like row.
 */
const MenuItem = ({leadingIcon, trailingIcon, secondaryLabel, destructive, disabled, closeOnClick, onClick, children}: ItemProps) => (
    <BaseMenu.Item
        className={classNames(styles.item, {
            [styles.destructive]: destructive,
            [styles.disabled]: disabled,
        })}
        disabled={disabled}
        closeOnClick={closeOnClick}
        onClick={onClick}
    >
        <ItemBody
            leadingIcon={leadingIcon}
            trailingIcon={trailingIcon}
            secondaryLabel={secondaryLabel}
        >
            {children}
        </ItemBody>
    </BaseMenu.Item>
);

/** A checkbox menu item that remains open while a set of options is edited. */
const MenuCheckboxItem = ({checked, secondaryLabel, disabled, onCheckedChange, children}: CheckboxItemProps) => (
    <BaseMenu.CheckboxItem
        className={classNames(styles.item, {[styles.disabled]: disabled})}
        checked={checked}
        disabled={disabled}
        closeOnClick={false}
        onCheckedChange={(next) => onCheckedChange(next)}
    >
        <ItemBody
            leadingIcon={(
                <BaseMenu.CheckboxItemIndicator
                    className={styles.checkboxIndicator}
                    keepMounted={true}
                >
                    <CheckIcon size={14}/>
                </BaseMenu.CheckboxItemIndicator>
            )}
            secondaryLabel={secondaryLabel}
        >
            {children}
        </ItemBody>
    </BaseMenu.CheckboxItem>
);

/** Groups mutually-exclusive `Menu.RadioItem` children; only one may be selected at a time. */
const MenuRadioGroup = ({value, onValueChange, children}: RadioGroupProps) => (
    <BaseMenu.RadioGroup
        value={value}
        onValueChange={(next) => onValueChange(next as string)}
    >
        {children}
    </BaseMenu.RadioGroup>
);

/**
 * One option within a `Menu.RadioGroup`. Exposes `role="menuitemradio"`/`aria-checked` so the
 * selected option is announced without depending on the visible check indicator alone.
 */
const MenuRadioItem = ({value, leadingIcon, secondaryLabel, disabled, children}: RadioItemProps) => (
    <BaseMenu.RadioItem
        value={value}
        className={classNames(styles.item, {[styles.disabled]: disabled})}
        disabled={disabled}
        closeOnClick={true}
    >
        <ItemBody
            leadingIcon={leadingIcon}
            trailingIcon={(
                <BaseMenu.RadioItemIndicator
                    className={styles.checkboxIndicator}
                    keepMounted={true}
                >
                    <CheckIcon size={14}/>
                </BaseMenu.RadioItemIndicator>
            )}
            secondaryLabel={secondaryLabel}
        >
            {children}
        </ItemBody>
    </BaseMenu.RadioItem>
);

/**
 * A menu item that navigates. Renders a real anchor for proper semantics,
 * keyboard and middle-click support.
 */
const MenuLinkItem = ({href, external, leadingIcon, trailingIcon, secondaryLabel, destructive, disabled, onClick, children}: LinkItemProps) => (
    <BaseMenu.Item
        className={classNames(styles.item, styles.link, {
            [styles.destructive]: destructive,
            [styles.disabled]: disabled,
        })}
        disabled={disabled}
        onClick={onClick}
        render={(
            <a
                href={href}
                {...(external ? {target: '_blank', rel: 'noopener noreferrer'} : {})}
            />
        )}
    >
        <ItemBody
            leadingIcon={leadingIcon}
            trailingIcon={trailingIcon}
            secondaryLabel={secondaryLabel}
        >
            {children}
        </ItemBody>
    </BaseMenu.Item>
);

/** A horizontal rule between groups of items. */
const MenuSeparator = () => <BaseMenu.Separator className={styles.divider}/>;

/** A nested menu opened from an item row. */
const MenuSubmenu = ({label, leadingIcon, ariaLabel, disabled, children}: SubmenuProps) => {
    const popupLabel = ariaLabel ?? (typeof label === 'string' ? label : undefined);
    return (
        <BaseMenu.SubmenuRoot>
            <BaseMenu.SubmenuTrigger
                className={classNames(styles.item, styles.submenuTrigger, {[styles.disabled]: disabled})}
                disabled={disabled}
            >
                <ItemBody
                    leadingIcon={leadingIcon}
                    trailingIcon={<ChevronRightIcon size={16}/>}
                >
                    {label}
                </ItemBody>
            </BaseMenu.SubmenuTrigger>
            <BaseMenu.Portal>
                <BaseMenu.Positioner
                    className={styles.positioner}
                    side='right'
                    align='start'
                    collisionPadding={8}
                >
                    <BaseMenu.Popup
                        className={styles.popover}
                        aria-label={popupLabel}

                        // As in Menu below: without this, Base UI's trigger-derived aria-labelledby
                        // outranks the aria-label. A popup with no label of its own keeps the
                        // trigger-derived name.
                        {...(popupLabel === undefined ? {} : {'aria-labelledby': undefined})}
                    >
                        {children}
                    </BaseMenu.Popup>
                </BaseMenu.Positioner>
            </BaseMenu.Portal>
        </BaseMenu.SubmenuRoot>
    );
};

/**
 * A dropdown menu built from `Menu.Item`, `Menu.LinkItem`, `Menu.Separator` and
 * `Menu.Submenu` children. Portals to the body so it is never clipped by an
 * ancestor's overflow.
 */
const Menu = ({ariaLabel, align = 'left', tooltip, trigger, open, onOpenChange, children}: MenuProps) => (
    <BaseMenu.Root
        open={open}
        onOpenChange={onOpenChange}
    >
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
                collisionPadding={8}
            >
                <BaseMenu.Popup
                    className={styles.popover}
                    aria-label={ariaLabel}

                    // Base UI points aria-labelledby at the trigger, and labelledby outranks
                    // aria-label in the accessible-name computation. Clearing it lets ariaLabel
                    // name the popup.
                    aria-labelledby={undefined}
                >
                    {children}
                </BaseMenu.Popup>
            </BaseMenu.Positioner>
        </BaseMenu.Portal>
    </BaseMenu.Root>
);

Menu.Item = MenuItem;
Menu.CheckboxItem = MenuCheckboxItem;
Menu.RadioGroup = MenuRadioGroup;
Menu.RadioItem = MenuRadioItem;
Menu.LinkItem = MenuLinkItem;
Menu.Separator = MenuSeparator;
Menu.Submenu = MenuSubmenu;

export default Menu;

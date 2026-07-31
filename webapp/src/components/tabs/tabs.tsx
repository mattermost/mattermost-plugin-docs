// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Tabs as BaseTabs} from '@base-ui-components/react/tabs';
import classNames from 'classnames';
import React from 'react';

import {Button} from 'components/form_controls/button';

import styles from './tabs.module.scss';

// Base UI Tabs give real tablist/tab/tabpanel semantics and arrow-key roving
// focus for free; each tab renders as our shared Button. Core's global
// `.btn + .btn { margin-left }` would then indent every tab after the first, so
// the list container zeroes that margin and spaces tabs with `gap` instead.

export type TabsOrientation = 'horizontal' | 'vertical';

type TabsProps = {
    value: string;
    onValueChange: (value: string) => void;
    orientation?: TabsOrientation;
    className?: string;
    children: React.ReactNode;
};

export const Tabs = ({value, onValueChange, orientation = 'horizontal', className, children}: TabsProps) => (
    <BaseTabs.Root
        value={value}
        onValueChange={(next) => onValueChange(String(next))}
        orientation={orientation}
        className={classNames(styles.root, styles[orientation], className)}
    >
        {children}
    </BaseTabs.Root>
);

type TabListProps = {
    className?: string;
    'aria-label'?: string;
    children: React.ReactNode;
};

export const TabList = ({className, children, ...rest}: TabListProps) => (
    <BaseTabs.List
        className={classNames(styles.list, className)}
        {...rest}
    >
        {children}
    </BaseTabs.List>
);

type TabProps = {
    value: string;
    leadingIcon?: React.ReactNode;
    destructive?: boolean;
    className?: string;
    children: React.ReactNode;
};

export const Tab = ({value, leadingIcon, destructive = false, className, children}: TabProps) => (
    <BaseTabs.Tab
        value={value}
        render={(
            <Button
                emphasis='quaternary'
                size='sm'
                leadingIcon={leadingIcon}
                className={classNames(styles.tab, {[styles.tabDestructive]: destructive}, className)}
            >
                <span className={styles.tabLabel}>{children}</span>
            </Button>
        )}
    />
);

type TabPanelProps = {
    value: string;
    className?: string;
    children: React.ReactNode;
};

export const TabPanel = ({value, className, children}: TabPanelProps) => (
    <BaseTabs.Panel
        value={value}
        className={classNames(styles.panel, className)}
    >
        {children}
    </BaseTabs.Panel>
);

/** Non-interactive visual separator between groups of tabs in a `TabList`. */
export const TabsSeparator = () => (
    <div
        className={styles.separator}
        role='presentation'
    />
);

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type React from 'react';

export type MenuItemSpec = {
    id: string;
    label: React.ReactNode;
    secondaryLabel?: React.ReactNode;
    leadingIcon?: React.ReactNode;
    onClick?: () => void;
    isDestructive?: boolean;
    isLink?: boolean;
    hasDivider?: boolean;

    // When set, the item renders as a real anchor (proper semantics, keyboard
    // and middle-click support) instead of a button. `external` opens in a new
    // tab with a safe rel.
    href?: string;
    external?: boolean;
};

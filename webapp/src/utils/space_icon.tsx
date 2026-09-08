// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import type {Space} from 'types/docs';

// A space's icon: its custom emoji when set, otherwise a glyph for its view policy — a globe when
// eligible team non-members may be admitted, a lock for members-only. An absent value reads as
// private, the fail-closed answer.
//
// The glyph deliberately differs from a page's (file-text/folder): spaces and
// pages sit side by side in the sidebar's favorites, so they must not look alike.
// `space` is optional so the create form can render the standard icon before a
// space exists.
type Props = {
    space?: Pick<Space, 'icon'> & Partial<Pick<Space, 'view_access'>>;
    size: number;
};

export function SpaceIcon({space, size}: Props): JSX.Element {
    if (space?.icon) {
        return <>{space.icon}</>;
    }
    if (space?.view_access === 'open') {
        return <GlobeIcon size={size}/>;
    }
    return <LockOutlineIcon size={size}/>;
}

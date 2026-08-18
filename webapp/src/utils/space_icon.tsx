// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import type {Space} from 'types/docs';

// A space's icon: its custom emoji when set, otherwise a glyph for its
// visibility — a globe for a public space, a lock for a private one. Visibility
// isn't persisted yet (no view_access on the server until PR #10), so an absent
// value reads as public. New spaces default to private in the create form.
//
// The glyph deliberately differs from a page's (file-text/folder): spaces and
// pages sit side by side in the sidebar's favorites, so they must not look alike.
// `space` is optional so the create form can render the standard icon before a
// space exists.
type Props = {
    space?: Pick<Space, 'icon' | 'visibility'>;
    size: number;
};

export function SpaceIcon({space, size}: Props): JSX.Element {
    if (space?.icon) {
        return <>{space.icon}</>;
    }
    if (space?.visibility === 'private') {
        return <LockOutlineIcon size={size}/>;
    }
    return <GlobeIcon size={size}/>;
}

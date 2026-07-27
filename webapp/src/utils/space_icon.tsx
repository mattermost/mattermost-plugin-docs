// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import FileTextOutlineIcon from '@mattermost/compass-icons/components/file-text-outline';

import type {Space} from 'types/docs';

// A space's icon: its custom emoji when set, otherwise a generic compass glyph
// (the product's file-text-outline). Custom emoji/icon picking is deferred, so
// most spaces render the fallback today. `space` is optional so the create
// form can render the standard icon before a space exists.
type Props = {
    space?: Pick<Space, 'icon'>;
    size: number;
};

export function SpaceIcon({space, size}: Props): JSX.Element {
    if (space?.icon) {
        return <>{space.icon}</>;
    }
    return <FileTextOutlineIcon size={size}/>;
}

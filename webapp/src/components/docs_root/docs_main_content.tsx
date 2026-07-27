// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpace} from 'hooks/spaces';
import React from 'react';

import DocsHome from 'components/docs_home/docs_home';
import SpaceView from 'components/space_view/space_view';

type Props = {
    spaceId?: string;
    pageId?: string;
    onCreateSpace: () => void;
    onBrowseSpaces: () => void;
};

// No routed space → the product Home; a routed space → its main content view
// (the page editor within it is mounted later).
const DocsMainContent = ({spaceId, onCreateSpace, onBrowseSpaces}: Props) => {
    const space = useSpace(spaceId);

    if (!space) {
        return (
            <DocsHome
                onCreateSpace={onCreateSpace}
                onBrowseSpaces={onBrowseSpaces}
            />
        );
    }

    return <SpaceView space={space}/>;
};

export default DocsMainContent;

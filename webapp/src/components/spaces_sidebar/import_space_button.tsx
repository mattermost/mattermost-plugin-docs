// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import DownloadOutlineIcon from '@mattermost/compass-icons/components/download-outline';

import SidebarItem from './sidebar_item';

type Props = {
    onClick?: () => void;
};

// ImportSpaceButton opens the Confluence import wizard.
//
// It sits beside "Create a space" because that is what it produces: an import into a new Space is another way
// of making one, and a user who has an export to bring across looks where new Spaces come from. Importing into
// an *existing* Space is a per-Space action and belongs on that Space instead, which is not built yet.
const ImportSpaceButton = ({onClick}: Props) => {
    const {formatMessage} = useIntl();
    const label = formatMessage({id: 'docs.sidebar.importSpace', defaultMessage: 'Import from Confluence'});

    return (
        <SidebarItem
            leading={<DownloadOutlineIcon size={16}/>}
            label={label}
            title={label}
            onClick={onClick}
        />
    );
};

export default ImportSpaceButton;

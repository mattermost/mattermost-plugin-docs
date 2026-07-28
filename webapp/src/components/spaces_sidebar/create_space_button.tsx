// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import PlusIcon from '@mattermost/compass-icons/components/plus';

import SidebarItem from './sidebar_item';

type Props = {
    onClick?: () => void;
};

const CreateSpaceButton = ({onClick}: Props) => {
    const {formatMessage} = useIntl();
    const label = formatMessage({id: 'docs.sidebar.createSpace', defaultMessage: 'Create a space'});

    return (
        <SidebarItem
            leading={<PlusIcon size={16}/>}
            label={label}
            title={label}
            onClick={onClick}
        />
    );
};

export default CreateSpaceButton;

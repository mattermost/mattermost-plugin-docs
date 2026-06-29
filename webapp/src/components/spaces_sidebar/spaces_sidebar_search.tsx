// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import MagnifyIcon from '@mattermost/compass-icons/components/magnify';

import './spaces_sidebar_search.scss';

type Props = {
    onOpen: () => void;
};

// Opens the Find-docs switcher (Cmd/Ctrl+K) rather than filtering inline,
// mirroring the channel switcher flow.
const SpacesSidebarSearch = ({onOpen}: Props) => {
    const {formatMessage} = useIntl();
    const label = formatMessage({id: 'docs.sidebar.search.placeholder', defaultMessage: 'Find docs'});

    return (
        <div className='DocsSidebarSearch'>
            <button
                type='button'
                className='DocsSidebarSearch__field'
                onClick={onOpen}
            >
                <MagnifyIcon size={16}/>
                <span className='DocsSidebarSearch__placeholder'>{label}</span>
            </button>
        </div>
    );
};

export default SpacesSidebarSearch;

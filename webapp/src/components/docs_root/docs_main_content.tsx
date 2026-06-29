// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpace} from 'hooks/use_space';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import './docs_main_content.scss';

type Props = {
    spaceId?: string;
    pageId?: string;
};

// Placeholder for the Docs center pane. The space landing / page view is built
// in a later ticket; for now it reflects the routed space/page so the URL
// structure is observable end to end.
const DocsMainContent = ({spaceId, pageId}: Props) => {
    const space = useSpace(spaceId);

    let body: React.ReactNode;
    if (space) {
        body = (
            <>
                <h2 className='DocsMain__title'>
                    <span aria-hidden={true}>{`${space.emoji} `}</span>
                    {space.name}
                </h2>
                <p className='DocsMain__subtitle'>
                    {pageId ? (
                        <FormattedMessage
                            id='docs.main.page'
                            defaultMessage='Page {pageId}'
                            values={{pageId}}
                        />
                    ) : (
                        <FormattedMessage
                            id='docs.main.spaceOverview'
                            defaultMessage='Space overview'
                        />
                    )}
                </p>
            </>
        );
    } else {
        body = (
            <>
                <h2 className='DocsMain__title'>
                    <FormattedMessage
                        id='docs.main.placeholder.title'
                        defaultMessage='Select a space to get started'
                    />
                </h2>
                <p className='DocsMain__subtitle'>
                    <FormattedMessage
                        id='docs.main.placeholder.subtitle'
                        defaultMessage='Spaces and pages will appear here.'
                    />
                </p>
            </>
        );
    }

    return (
        <div className='DocsMain'>
            <div className='DocsMain__empty'>{body}</div>
        </div>
    );
};

export default DocsMainContent;

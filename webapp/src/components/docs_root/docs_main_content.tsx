// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useRoutedSpace} from 'hooks/spaces';
import React from 'react';
import {Redirect, Route, Switch} from 'react-router-dom';
import {DOCS_DRAFT_ROUTE, DOCS_SPACE_OVERVIEW_ROUTE, DOCS_SPACE_ROUTE} from 'routing/paths';

import DocsHome from 'components/docs_home/docs_home';
import SpaceView from 'components/space_view/space_view';

type Props = {
    onCreateSpace: () => void;
    onBrowseSpaces: () => void;
};

// Routes the product's main column: a routed space renders its view, the product
// root (anything else) renders Home.
const DocsMainContent = ({onCreateSpace, onBrowseSpaces}: Props) => {
    const home = (
        <DocsHome
            onCreateSpace={onCreateSpace}
            onBrowseSpaces={onBrowseSpaces}
        />
    );

    return (
        <Switch>
            {/* The draft pattern is listed first: the generic space route would
                otherwise capture 'drafts' as the page id. Both render the space
                view, which reads the parsed selection from the URL. */}
            <Route path={[DOCS_DRAFT_ROUTE, DOCS_SPACE_OVERVIEW_ROUTE, DOCS_SPACE_ROUTE]}>
                <RoutedSpaceView/>
            </Route>
            <Route>{home}</Route>
        </Switch>
    );
};

// Resolves the routed space id, asking the server for that id when the store
// doesn't hold it (see useRoutedSpace) rather than treating the team listing as
// the last word. Once the id has an answer and there's no space, it names one the
// user can't see or one that's gone, so the URL is corrected to the product home;
// until then nothing renders rather than flashing Home.
const RoutedSpaceView = () => {
    const {spaceId, paths} = useDocsNavigation();
    const {space, resolved} = useRoutedSpace(spaceId);

    if (space) {
        return <SpaceView space={space}/>;
    }
    if (resolved) {
        return <Redirect to={paths.home()}/>;
    }
    return null;
};

export default DocsMainContent;

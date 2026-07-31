// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useAppSelector} from 'hooks/redux';
import {useSpace} from 'hooks/spaces';
import React from 'react';
import {Redirect, Route, Switch} from 'react-router-dom';
import {DOCS_DRAFT_ROUTE, DOCS_SPACE_OVERVIEW_ROUTE, DOCS_SPACE_ROUTE} from 'routing/paths';

import {areSpacesLoadedForCurrentTeam} from 'store/selectors';

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

// Resolves the routed space id against the store. Once the team's spaces are
// loaded, an id that still isn't there names a space the user can't see (or one
// that's gone), so the URL is corrected to the product home; until then the id
// may simply not have arrived, so nothing renders rather than flashing Home.
const RoutedSpaceView = () => {
    const {spaceId, paths} = useDocsNavigation();
    const space = useSpace(spaceId);
    const spacesLoaded = useAppSelector(areSpacesLoadedForCurrentTeam);

    if (space) {
        return <SpaceView space={space}/>;
    }
    if (spacesLoaded) {
        return <Redirect to={paths.home()}/>;
    }
    return null;
};

export default DocsMainContent;

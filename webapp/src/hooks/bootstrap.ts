// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch} from 'hooks/redux';
import {useEffect} from 'react';

import {fetchSpaces} from 'store/actions';

// Loads the Docs data layer once the product mounts (i.e. an authenticated user
// has navigated into Docs), rather than at plugin init. Team-scoped: the server
// returns the current team's spaces the caller belongs to.
export function useBootstrapDocs(): void {
    const dispatch = useAppDispatch();

    useEffect(() => {
        dispatch(fetchSpaces());
    }, [dispatch]);
}

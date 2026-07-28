// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch} from 'hooks/redux';
import {useEffect} from 'react';

import {fetchPages, fetchSpaces} from 'store/actions';

// Loads the Docs data layer once the product mounts (i.e. an authenticated user
// has navigated into Docs), rather than at plugin init. Later this becomes a
// team-scoped fetch against the real API.
export function useBootstrapDocs(): void {
    const dispatch = useAppDispatch();

    useEffect(() => {
        dispatch(fetchSpaces());
        dispatch(fetchPages());
    }, [dispatch]);
}

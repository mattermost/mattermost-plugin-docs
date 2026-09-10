// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getDraftForPage} from 'store/selectors';

import {useOwnPageWrite} from './own_page_writes';
import {useAppSelector} from './redux';

export function useEditBaseline(pageId: string, loaded?: number): number | undefined {
    const draft = useAppSelector((state) => getDraftForPage(state, pageId));
    const own = useOwnPageWrite(pageId);

    return draft?.base_edit_at || own || loaded || undefined;
}

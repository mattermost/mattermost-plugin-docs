// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useRef} from 'react';

// Keeps a ref pointing at the latest value so long-lived callbacks (e.g. a
// drag monitor registered once) read current state without re-registering.
export function useLatest<T>(value: T) {
    const ref = useRef(value);
    ref.current = value;
    return ref;
}

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {announce, clearReadout, getReadoutState} from './readout_store';

describe('readout store', () => {
    afterEach(() => clearReadout());

    it('does not let an older announcement clear a newer one', () => {
        announce('First');
        const firstNonce = getReadoutState().nonce;
        announce('Second');

        clearReadout(firstNonce);

        expect(getReadoutState().message).toBe('Second');
    });
});

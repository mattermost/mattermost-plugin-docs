// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {flushPendingSaves, registerPendingSave} from './pending_saves';

describe('pending saves', () => {
    it('reports success when there is nothing waiting to be written', async () => {
        await expect(flushPendingSaves()).resolves.toBe(true);
    });

    it('runs every registered flush', async () => {
        const first = jest.fn().mockResolvedValue(true);
        const second = jest.fn().mockResolvedValue(true);

        const unregister = [registerPendingSave(first), registerPendingSave(second)];

        await expect(flushPendingSaves()).resolves.toBe(true);
        expect(first).toHaveBeenCalled();
        expect(second).toHaveBeenCalled();

        unregister.forEach((remove) => remove());
    });

    it('reports failure when a flush could not write, so the caller does not publish without it', async () => {
        const remove = registerPendingSave(jest.fn().mockResolvedValue(false));

        await expect(flushPendingSaves()).resolves.toBe(false);

        remove();
    });

    it('stops running a flush once its editor is gone', async () => {
        const flush = jest.fn().mockResolvedValue(true);

        registerPendingSave(flush)();

        await flushPendingSaves();
        expect(flush).not.toHaveBeenCalled();
    });
});

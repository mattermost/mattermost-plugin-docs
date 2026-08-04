// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, renderHook} from '@testing-library/react';
import {updatePageDraft} from 'client/drafts';

import type {Draft, DraftPatch} from 'types/drafts';

import {AUTOSAVE_DEBOUNCE_MS, useDraftAutosave} from './draft_autosave';

jest.mock('client/drafts', () => ({
    updatePageDraft: jest.fn(),
}));

const mockUpdate = updatePageDraft as jest.MockedFunction<typeof updatePageDraft>;

const savedDraft = {page_id: 'page1'} as Draft;

const patchesSent = (): DraftPatch[] => mockUpdate.mock.calls.map((call) => call[2]);

const setup = (overrides: Partial<Parameters<typeof useDraftAutosave>[0]> = {}) =>
    renderHook((props: Parameters<typeof useDraftAutosave>[0]) => useDraftAutosave(props), {
        initialProps: {
            spaceId: 'space1',
            pageId: 'page1',
            enabled: true,
            ...overrides,
        },
    });

const runDebounce = async () => {
    await act(async () => {
        jest.advanceTimersByTime(AUTOSAVE_DEBOUNCE_MS);
    });
};

beforeEach(() => {
    jest.useFakeTimers();
    mockUpdate.mockReset();
    mockUpdate.mockResolvedValue(savedDraft);
});

afterEach(() => {
    jest.useRealTimers();
});

describe('useDraftAutosave', () => {
    it('debounces bursts into a single write', async () => {
        const {result} = setup();

        act(() => {
            result.current.queue({body: 'a'});
            result.current.queue({body: 'ab'});
            result.current.queue({body: 'abc'});
        });
        expect(mockUpdate).not.toHaveBeenCalled();

        await runDebounce();

        expect(mockUpdate).toHaveBeenCalledTimes(1);
        expect(patchesSent()[0]).toEqual({body: 'abc'});
    });

    it('coalesces different fields rather than replacing the pending patch', async () => {
        const {result} = setup();

        act(() => {
            result.current.queue({title: 'Title'});
            result.current.queue({body: 'Body'});
        });
        await runDebounce();

        expect(mockUpdate).toHaveBeenCalledTimes(1);
        expect(patchesSent()[0]).toEqual({title: 'Title', body: 'Body'});
    });

    it('repeats base_edit_at on every write for an existing page', async () => {
        const {result} = setup({baseEditAt: 1234});

        act(() => {
            result.current.queue({body: 'first'});
        });
        await runDebounce();

        act(() => {
            result.current.queue({body: 'second'});
        });
        await runDebounce();

        expect(patchesSent()).toEqual([
            {body: 'first', base_edit_at: 1234},
            {body: 'second', base_edit_at: 1234},
        ]);
    });

    it('omits base_edit_at for a new-page draft, which has no baseline', async () => {
        const {result} = setup();

        act(() => {
            result.current.queue({body: 'new'});
        });
        await runDebounce();

        expect(mockUpdate).toHaveBeenCalledTimes(1);
        expect(patchesSent()[0]).not.toHaveProperty('base_edit_at');
    });

    it('does not write while disabled, so a new page cannot autosave before its id exists', async () => {
        const {result} = setup({enabled: false});

        act(() => {
            result.current.queue({body: 'a'});
        });
        await runDebounce();

        expect(mockUpdate).not.toHaveBeenCalled();
    });

    it('cancel drops a pending save so discard is not undone by the debounce', async () => {
        const {result} = setup();

        act(() => {
            result.current.queue({body: 'doomed'});
            result.current.cancel();
        });
        await runDebounce();

        expect(mockUpdate).not.toHaveBeenCalled();
        expect(result.current.status).toBe('saved');
    });

    it('ignores an in-flight save that resolves after cancel', async () => {
        let resolveSave: (draft: Draft) => void = () => {};
        mockUpdate.mockReturnValueOnce(new Promise<Draft>((resolve) => {
            resolveSave = resolve;
        }));

        const onSaved = jest.fn();
        const {result} = setup({onSaved});

        act(() => {
            result.current.queue({body: 'inflight'});
        });
        await runDebounce();
        expect(mockUpdate).toHaveBeenCalledTimes(1);

        act(() => {
            result.current.cancel();
        });
        await act(async () => {
            resolveSave(savedDraft);
        });

        expect(onSaved).not.toHaveBeenCalled();
        expect(result.current.status).toBe('saved');
    });

    it('flush writes immediately so publish does not race the debounce', async () => {
        const {result} = setup();

        act(() => {
            result.current.queue({body: 'pending'});
        });
        await act(async () => {
            await result.current.flush();
        });

        expect(mockUpdate).toHaveBeenCalledTimes(1);
        expect(patchesSent()[0]).toEqual({body: 'pending'});
    });

    it('keeps the patch for retry when a save fails', async () => {
        mockUpdate.mockRejectedValueOnce(new Error('offline'));
        const onError = jest.fn();
        const {result} = setup({onError});

        act(() => {
            result.current.queue({body: 'lost'});
        });
        await runDebounce();

        expect(onError).toHaveBeenCalledTimes(1);
        expect(result.current.status).toBe('unsaved');

        await act(async () => {
            await result.current.flush();
        });
        expect(patchesSent()[1]).toEqual({body: 'lost'});
    });

    it('stays dirty when edits arrive while a save is in flight', async () => {
        let resolveSave: (draft: Draft) => void = () => {};
        mockUpdate.mockReturnValueOnce(new Promise<Draft>((resolve) => {
            resolveSave = resolve;
        }));

        const {result} = setup();

        act(() => {
            result.current.queue({body: 'first'});
        });
        await runDebounce();

        act(() => {
            result.current.queue({body: 'second'});
        });
        await act(async () => {
            resolveSave(savedDraft);
        });

        expect(result.current.status).toBe('unsaved');
    });

    it('flush reports failure so publish does not proceed on unsaved content', async () => {
        mockUpdate.mockRejectedValueOnce(new Error('offline'));
        const {result} = setup();

        act(() => {
            result.current.queue({body: 'lost'});
        });

        let flushed: boolean | undefined;
        await act(async () => {
            flushed = await result.current.flush();
        });

        expect(flushed).toBe(false);
        expect(result.current.status).toBe('unsaved');
    });

    it('flush waits for a write already in flight instead of resolving early', async () => {
        let resolveSave: (draft: Draft) => void = () => {};
        mockUpdate.mockReturnValueOnce(new Promise<Draft>((resolve) => {
            resolveSave = resolve;
        }));

        const {result} = setup();

        act(() => {
            result.current.queue({body: 'inflight'});
        });
        await runDebounce();
        expect(mockUpdate).toHaveBeenCalledTimes(1);

        let settled = false;
        let flushed: Promise<boolean> = Promise.resolve(false);
        act(() => {
            flushed = result.current.flush().then((ok) => {
                settled = true;
                return ok;
            });
        });

        await act(async () => {
            await Promise.resolve();
        });
        expect(settled).toBe(false);

        await act(async () => {
            resolveSave(savedDraft);
            await flushed;
        });
        expect(settled).toBe(true);
    });

    it('does not merge a failed patch into a patch queued for another page', async () => {
        let rejectSave: (error: Error) => void = () => {};
        mockUpdate.mockReturnValueOnce(new Promise<Draft>((_, reject) => {
            rejectSave = reject;
        }));

        const {result, rerender} = setup();

        act(() => {
            result.current.queue({body: 'page1 body'});
        });
        await runDebounce();
        expect(mockUpdate).toHaveBeenCalledTimes(1);

        act(() => {
            rerender({spaceId: 'space1', pageId: 'page2', enabled: true});
        });
        act(() => {
            result.current.queue({body: 'page2 body'});
        });

        await act(async () => {
            rejectSave(new Error('offline'));
        });

        await act(async () => {
            await result.current.flush();
        });

        const page2Writes = mockUpdate.mock.calls.filter((call) => call[1] === 'page2');
        expect(page2Writes).toHaveLength(1);
        expect(page2Writes[0][2]).toEqual({body: 'page2 body'});

        for (const call of mockUpdate.mock.calls) {
            if (call[1] === 'page1') {
                expect(call[2]).not.toMatchObject({body: 'page2 body'});
            }
        }
    });

    it('sends the baseline of the page it targets, not the page now on screen', async () => {
        const {result, rerender} = setup({baseEditAt: 111});

        act(() => {
            result.current.queue({body: 'typed on page1'});
        });

        await act(async () => {
            rerender({spaceId: 'space1', pageId: 'page2', enabled: true, baseEditAt: 222});
        });

        expect(mockUpdate.mock.calls[0][1]).toBe('page1');
        expect(patchesSent()[0]).toEqual({body: 'typed on page1', base_edit_at: 111});
    });

    it('flushes the pending patch to the page being left when the id changes', async () => {
        const {result, rerender} = setup();

        act(() => {
            result.current.queue({body: 'typed on page1'});
        });

        await act(async () => {
            rerender({spaceId: 'space1', pageId: 'page2', enabled: true});
        });

        expect(mockUpdate).toHaveBeenCalledTimes(1);
        expect(mockUpdate.mock.calls[0][1]).toBe('page1');
        expect(patchesSent()[0]).toEqual({body: 'typed on page1'});
    });

    it('flushes the pending patch even when the next page reports itself as loading', async () => {
        const {result, rerender} = setup();

        act(() => {
            result.current.queue({body: 'typed on page1'});
        });

        await act(async () => {
            rerender({spaceId: 'space1', pageId: 'page2', enabled: false});
        });

        expect(mockUpdate).toHaveBeenCalledTimes(1);
        expect(mockUpdate.mock.calls[0][1]).toBe('page1');
        expect(patchesSent()[0]).toEqual({body: 'typed on page1'});
    });

    it('flushes the pending patch when the editor unmounts', async () => {
        const {result, unmount} = setup();

        act(() => {
            result.current.queue({body: 'typed before leaving'});
        });

        await act(async () => {
            unmount();
        });

        expect(mockUpdate).toHaveBeenCalledTimes(1);
        expect(patchesSent()[0]).toEqual({body: 'typed before leaving'});
    });
});

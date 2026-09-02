// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import '@testing-library/jest-dom';

// Base UI waits for a popup's animations to finish before reporting a close as
// complete, which is how a modal gets to animate out. jsdom runs no animations, so
// that wait never resolves and the close never completes. Base UI's own escape
// hatch (see utils/useAnimationsFinished) makes it run the callback immediately.
(globalThis as {BASE_UI_ANIMATIONS_DISABLED?: boolean}).BASE_UI_ANIMATIONS_DISABLED = true;

// useTeamContext's module also imports team actions (for useEnsureCurrentTeam);
// the real action tree pulls in ESM-only deps jest doesn't transform. No test
// exercises team actions, so stub the module everywhere.
jest.mock('mattermost-redux/actions/teams', () => ({
    selectTeam: () => ({type: 'MOCK_SELECT_TEAM'}),
}));

// WithTooltip is host chrome that pulls in the shared platform/emoji context
// tree; render its children directly in tests.
jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => children,
}));

// Base UI (Dialog/Menu) observes elements for positioning; jsdom has neither.
class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
global.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;

// jsdom doesn't implement DOMRect (used by floating element positioning).
if (!global.DOMRect) {
    global.DOMRect = class {
        x = 0;
        y = 0;
        width = 0;
        height = 0;
        top = 0;
        right = 0;
        bottom = 0;
        left = 0;
        toJSON() {
            return {};
        }
    } as unknown as typeof DOMRect;
}

// jsdom doesn't implement scrollIntoView (used by list keyboard navigation).
if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
}

if (!window.matchMedia) {
    window.matchMedia = ((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: () => {},
        removeEventListener: () => {},
        addListener: () => {},
        removeListener: () => {},
        dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;
}

export {};

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The body a spec writes to cover the editor's text formats, shared so the typing
// and the assertions cannot drift apart.
//
// Docs mounts the host editor with contentType='json', which disables both the
// Markdown extension and the markdown paste handler. Formats therefore have to come
// from TipTap's input rules as the author types — which is what a real author does.
export interface RichText {
    heading1: string;
    heading2: string;
    bold: string;
    italic: string;
    strike: string;
    inlineCode: string;
    quote: string;
    bullets: [string, string];
    ordered: [string, string];
    code: string;
}

export function richText(suffix: string): RichText {
    return {
        // Carries the run's suffix so a retry cannot match a previous attempt's page.
        heading1: `Heading one ${suffix}`,
        heading2: 'Heading two',
        bold: 'bold words',
        italic: 'italic words',
        strike: 'struck words',
        inlineCode: 'inlineCode()',
        quote: 'A quoted line',
        bullets: ['First bullet', 'Second bullet'],
        ordered: ['First step', 'Second step'],
        code: 'const answer = 42;',
    };
}

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Shared so typing and assertions cannot drift. Docs mounts the editor with
// contentType='json', which disables the Markdown extension and paste handler, so
// formats have to come from TipTap's input rules as the author types.
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
        // Suffixed so a retry cannot match a previous attempt's page.
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

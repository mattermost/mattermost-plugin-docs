// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Copies text to the clipboard, preferring the async Clipboard API and falling
// back to a hidden textarea + execCommand on browsers/contexts without it (or
// when writeText rejects — e.g. permissions or an insecure context).
// Mirrors the core webapp's utils.copyToClipboard, resolving to whether the text
// made it, so callers can confirm a copy that actually happened.
export function copyToClipboard(text: string): Promise<boolean> {
    if (navigator.clipboard) {
        return navigator.clipboard.writeText(text).
            then(() => true).
            catch(() => legacyCopy(text));
    }

    return Promise.resolve(legacyCopy(text));
}

function legacyCopy(text: string): boolean {
    const textArea = document.createElement('textarea');
    textArea.style.position = 'fixed';
    textArea.style.top = '0';
    textArea.style.left = '0';
    textArea.style.width = '1px';
    textArea.style.height = '1px';
    textArea.style.padding = '0';
    textArea.style.border = 'none';
    textArea.style.outline = 'none';
    textArea.style.boxShadow = 'none';
    textArea.style.background = 'transparent';
    textArea.value = text;
    document.body.appendChild(textArea);
    textArea.select();

    let copied = false;
    try {
        copied = document.execCommand('copy');
    } catch {
        copied = false;
    }
    document.body.removeChild(textArea);

    return copied;
}

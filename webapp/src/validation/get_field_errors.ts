// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {z} from 'zod';

// Maps a Zod validation failure to the first error message per field, the shape
// most form UIs want for inline error display.
export function getFieldErrors(error: z.ZodError): Record<string, string> {
    const fieldErrors = z.flattenError(error).fieldErrors as Record<string, string[] | undefined>;
    const result: Record<string, string> = {};
    for (const [field, messages] of Object.entries(fieldErrors)) {
        if (messages && messages.length > 0) {
            result[field] = messages[0];
        }
    }
    return result;
}

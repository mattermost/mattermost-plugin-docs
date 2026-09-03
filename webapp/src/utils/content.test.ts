// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {sameContent} from './content';

describe('sameContent', () => {
    it('matches documents that differ only in key order', () => {
        const stored = '{"content":[{"attrs":{"level":1},"type":"heading"}],"type":"doc"}';
        const emitted = '{"type":"doc","content":[{"type":"heading","attrs":{"level":1}}]}';

        expect(sameContent(stored, emitted)).toBe(true);
    });

    it('separates documents whose content differs', () => {
        const one = '{"type":"doc","content":[{"type":"text","text":"a"}]}';
        const other = '{"type":"doc","content":[{"type":"text","text":"b"}]}';

        expect(sameContent(one, other)).toBe(false);
    });

    it('keeps array order significant', () => {
        const one = '{"content":[{"text":"a"},{"text":"b"}]}';
        const other = '{"content":[{"text":"b"},{"text":"a"}]}';

        expect(sameContent(one, other)).toBe(false);
    });

    it('keeps a "__proto__" key as content rather than losing it to the prototype', () => {
        const withKey = '{"attrs":{"__proto__":"x"},"type":"doc"}';
        const without = '{"attrs":{},"type":"doc"}';

        expect(sameContent(withKey, without)).toBe(false);
    });

    it('falls back to exact comparison for content that is not JSON', () => {
        expect(sameContent('# Heading', '# Heading')).toBe(true);
        expect(sameContent('# Heading', '# Other')).toBe(false);
    });
});

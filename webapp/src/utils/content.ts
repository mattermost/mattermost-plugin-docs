// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

const FAILED = Symbol('unparsed');

export function sameContent(left: string, right: string): boolean {
    if (left === right) {
        return true;
    }

    const a = parse(left);
    const b = parse(right);
    if (a === FAILED || b === FAILED) {
        return false;
    }

    return JSON.stringify(a) === JSON.stringify(b);
}

function parse(content: string): unknown {
    try {
        return canonical(JSON.parse(content));
    } catch {
        return FAILED;
    }
}

function canonical(value: unknown): unknown {
    if (Array.isArray(value)) {
        return value.map(canonical);
    }

    if (value && typeof value === 'object') {
        const source = value as Record<string, unknown>;
        return Object.keys(source).sort().reduce<Record<string, unknown>>((out, key) => {
            out[key] = canonical(source[key]);
            return out;
        }, Object.create(null) as Record<string, unknown>);
    }

    return value;
}

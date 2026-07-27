// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import {Button} from '@mattermost/shared/components/button';
import type {ButtonProps} from '@mattermost/shared/components/button';

// The Docs plugin avoids styled-components (its browser ESM references `process`
// and throws in the plugin runtime), so these are plain functional wrappers that
// bind the emphasis/variant preset.

// The preset is applied first so an explicit prop can still override it.
function createButton(displayName: string, preset: Pick<ButtonProps, 'emphasis' | 'variant'>) {
    const Wrapped = React.forwardRef<HTMLButtonElement, ButtonProps>((props, ref) => (
        <Button
            ref={ref}
            {...preset}
            {...props}
        />
    ));
    Wrapped.displayName = displayName;
    return Wrapped;
}

export const PrimaryButton = createButton('PrimaryButton', {emphasis: 'primary'});
export const SecondaryButton = createButton('SecondaryButton', {emphasis: 'secondary'});
export const TertiaryButton = createButton('TertiaryButton', {emphasis: 'tertiary'});
export const DestructiveButton = createButton('DestructiveButton', {emphasis: 'primary', variant: 'destructive'});

export {Button};
export type {ButtonProps};

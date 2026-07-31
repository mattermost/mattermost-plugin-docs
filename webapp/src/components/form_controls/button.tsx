// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';

import {Button as SharedButton} from '@mattermost/shared/components/button';
import type {ButtonProps as SharedButtonProps} from '@mattermost/shared/components/button';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

// Neutral-grey treatment for `btn-icon` and `docs-btn-neutral` ghost buttons
// (the shared Button is accent-only), plus the badge overlay; the :global rules
// apply wherever these buttons render.
import styles from './button_neutral.module.scss';

// The Docs plugin avoids styled-components (its browser ESM references `process`
// and throws in the plugin runtime), so these are plain functional wrappers that
// bind the emphasis/variant preset.

type ContentProps = Omit<SharedButtonProps, 'children'> & {

    /** Icon rendered before `children`. */
    leadingIcon?: React.ReactNode;

    /** Icon rendered after `children`. */
    trailingIcon?: React.ReactNode;

    /**
     * Overlay indicator. `true` renders an unread dot; a number or string
     * renders a count pill.
     */
    badge?: number | string | true;

    /**
     * Wraps the button in a hover tooltip. A string tooltip also supplies the
     * accessible name when `aria-label` is omitted.
     */
    tooltip?: React.ReactNode;
};

// Icon-only buttons (no children) carry no text, so they must name themselves.
type LabelledProps = {children: React.ReactNode};
type IconOnlyProps = {children?: undefined} & ({'aria-label': string} | {tooltip: string});

export type ButtonProps = ContentProps & (LabelledProps | IconOnlyProps);

// The preset is applied first so an explicit prop can still override it.
function createButton(displayName: string, preset: Pick<SharedButtonProps, 'emphasis' | 'variant'>) {
    const Wrapped = React.forwardRef<HTMLButtonElement, ButtonProps>((props, ref) => {
        const {
            'aria-label': ariaLabel,
            badge,
            children,
            className,
            leadingIcon,
            tooltip,
            trailingIcon,
            ...rest
        } = props;

        const button = (
            <SharedButton
                ref={ref}
                type='button'
                {...preset}
                {...rest}
                aria-label={ariaLabel ?? (typeof tooltip === 'string' ? tooltip : undefined)}
                className={classNames(className, {[styles.badged]: badge !== undefined})}
            >
                {leadingIcon}
                {children}
                {trailingIcon}
                {badge === true && (
                    <span
                        className={styles.badgeDot}
                        aria-hidden='true'
                    />
                )}
                {badge !== undefined && badge !== true && (
                    <span className={styles.badgeCount}>{badge}</span>
                )}
            </SharedButton>
        );

        return tooltip ? <WithTooltip title={tooltip}>{button}</WithTooltip> : button;
    });
    Wrapped.displayName = displayName;
    return Wrapped;
}

export const Button = createButton('Button', {});
export const PrimaryButton = createButton('PrimaryButton', {emphasis: 'primary'});
export const SecondaryButton = createButton('SecondaryButton', {emphasis: 'secondary'});
export const TertiaryButton = createButton('TertiaryButton', {emphasis: 'tertiary'});
export const DestructiveButton = createButton('DestructiveButton', {emphasis: 'primary', variant: 'destructive'});

export type {SharedButtonProps};

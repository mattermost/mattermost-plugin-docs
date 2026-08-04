// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Input as BaseInput} from '@base-ui-components/react/input';
import classNames from 'classnames';
import React from 'react';
import {useIntl} from 'react-intl';

import styles from './page_title.module.scss';

// The server's model.PageTitleMaxRunes.
const TITLE_MAX_LENGTH = 255;

type Props = {
    value: string;
    editing: boolean;
    onChange: (value: string) => void;
    onCommit: () => void;
    onCancel: () => void;
};

/**
 * The page's title, as a heading while reading and as a field while editing. The
 * two are exclusive: view mode renders no focusable control, so a page the reader
 * cannot change offers nothing to tab into.
 *
 * `onCommit` and `onCancel` are named for intent rather than for the events that
 * raise them, because which events those are belongs to this component: both blur
 * and Enter commit, and Escape cancels.
 */
const PageTitle = ({value, editing, onChange, onCommit, onCancel}: Props) => {
    const {formatMessage} = useIntl();
    const untitled = formatMessage({id: 'docs.page.untitled', defaultMessage: 'Untitled'});

    if (!editing) {
        return (
            <h1 className={classNames(styles.title, {[styles.untitled]: !value})}>
                {value || untitled}
            </h1>
        );
    }

    return (
        <BaseInput
            className={classNames(styles.title, styles.field)}
            value={value}
            maxLength={TITLE_MAX_LENGTH}
            placeholder={untitled}
            aria-label={formatMessage({id: 'docs.page.titleLabel', defaultMessage: 'Page title'})}

            // The field mounts only on entering edit mode, so this focuses on the
            // gesture that asked to edit rather than on page load. Without it the
            // field is unreachable by keyboard without tabbing the whole page tree,
            // which sits between the Edit control and here in DOM order.
            autoFocus={true}

            // Not `onValueChange={onChange}`: Base UI also passes event details,
            // and onChange's contract is the value alone.
            onValueChange={(newValue) => onChange(newValue)}
            onBlur={onCommit}
            onKeyDown={(e) => {
                if (e.key === 'Enter') {
                    // A title is one line; Enter means "done", not "new line".
                    e.preventDefault();
                    onCommit();
                }
                if (e.key === 'Escape') {
                    e.preventDefault();
                    onCancel();
                }
            }}
        />
    );
};

export default PageTitle;

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useDocsSearch, useRecentDocs} from 'hooks/docs';
import {useDocsNavigation} from 'hooks/navigation';
import {useSpaces} from 'hooks/spaces';
import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import MagnifyIcon from '@mattermost/compass-icons/components/magnify';
import TextBoxOutlineIcon from '@mattermost/compass-icons/components/text-box-outline';

import GenericModal from 'components/generic_modal/generic_modal';

import type {Page, Space} from 'types/docs';

import styles from './docs_switcher.module.scss';

type Props = {
    onClose: () => void;
};

type Entry =
    | {key: string; index: number; kind: 'space'; space: Space}
    | {key: string; index: number; kind: 'page'; page: Page};

type Group = {id: string; title: string; entries: Entry[]};

const LISTBOX_ID = 'docs-switcher-listbox';
const optionId = (index: number) => `docs-switcher-option-${index}`;

const DocsSwitcher = ({onClose}: Props) => {
    const {formatMessage} = useIntl();
    const {navigate} = useDocsNavigation();
    const [query, setQuery] = useState('');
    const [activeIndex, setActiveIndex] = useState(0);
    const inputRef = useRef<HTMLInputElement>(null);
    const trimmed = query.trim().toLowerCase();
    const hasQuery = trimmed.length > 0;

    const allSpaces = useSpaces();
    const recent = useRecentDocs();
    const results = useDocsSearch(query);
    const spaceTitleById = useMemo(() => new Map(allSpaces.map((space) => [space.id, space.title])), [allSpaces]);

    const groups: Group[] = useMemo(() => {
        let i = 0;
        const spaceEntry = (space: Space): Entry => ({key: `space:${space.id}`, index: i++, kind: 'space', space});
        const pageEntry = (page: Page): Entry => ({key: `page:${page.id}`, index: i++, kind: 'page', page});

        if (hasQuery) {
            return [{
                id: 'results',
                title: formatMessage({id: 'docs.switcher.group.results', defaultMessage: 'Spaces and pages'}),
                entries: [...results.spaces.map(spaceEntry), ...results.pages.map(pageEntry)],
            }];
        }

        return [
            {
                id: 'recent',
                title: formatMessage({id: 'docs.switcher.group.recent', defaultMessage: 'Recent docs'}),
                entries: [...recent.spaces.map(spaceEntry), ...recent.pages.map(pageEntry)],
            },
            {
                id: 'spaces',
                title: formatMessage({id: 'docs.switcher.group.spaces', defaultMessage: 'Your spaces'}),
                entries: allSpaces.map(spaceEntry),
            },
        ];
    }, [hasQuery, results, recent, allSpaces, formatMessage]);

    const flat = useMemo(() => groups.flatMap((g) => g.entries), [groups]);
    const total = flat.length;
    const active = total === 0 ? -1 : Math.min(activeIndex, total - 1);

    useEffect(() => setActiveIndex(0), [trimmed]);

    useEffect(() => {
        if (active >= 0) {
            document.getElementById(optionId(active))?.scrollIntoView({block: 'nearest'});
        }
    }, [active]);

    const selectEntry = useCallback((entry?: Entry) => {
        if (!entry) {
            return;
        }
        if (entry.kind === 'space') {
            navigate(entry.space.id);
        } else {
            navigate(entry.page.space_id, entry.page.id);
        }
        onClose();
    }, [navigate, onClose]);

    const onInputKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (total === 0) {
            return;
        }
        switch (e.key) {
        case 'ArrowDown':
            e.preventDefault();
            setActiveIndex((prev) => (Math.min(prev, total - 1) + 1) % total);
            break;
        case 'ArrowUp':
            e.preventDefault();
            setActiveIndex((prev) => ((Math.min(prev, total - 1) - 1) + total) % total);
            break;
        case 'Home':
            e.preventDefault();
            setActiveIndex(0);
            break;
        case 'End':
            e.preventDefault();
            setActiveIndex(total - 1);
            break;
        case 'Enter':
            e.preventDefault();
            selectEntry(flat[active]);
            break;
        }
    };

    const title = hasQuery ? formatMessage({id: 'docs.switcher.title.query', defaultMessage: 'Find spaces or pages'}) : formatMessage({id: 'docs.switcher.title', defaultMessage: 'Find docs'});
    const placeholder = formatMessage({id: 'docs.switcher.placeholder', defaultMessage: 'Search all spaces and pages'});

    const renderEntry = (entry: Entry) => (
        <button
            key={entry.key}
            id={optionId(entry.index)}
            type='button'
            role='option'
            aria-selected={entry.index === active}
            className={classNames(styles.item, {[styles.active]: entry.index === active})}
            onMouseMove={() => setActiveIndex(entry.index)}
            onClick={() => selectEntry(entry)}
        >
            {entry.kind === 'space' ? (
                <>
                    <span className={classNames(styles.itemIcon, styles.itemIconEmoji)}>{entry.space.icon}</span>
                    <span className={styles.itemLabel}>{entry.space.title}</span>
                </>
            ) : (
                <>
                    <span className={styles.itemIcon}><TextBoxOutlineIcon size={16}/></span>
                    <span className={styles.itemLabel}>{entry.page.title}</span>
                    <span className={styles.itemMeta}>{spaceTitleById.get(entry.page.space_id)}</span>
                </>
            )}
        </button>
    );

    const searchField = (
        <div className={styles.inputWrap}>
            <MagnifyIcon size={16}/>
            <input
                ref={inputRef}
                className={styles.input}
                type='text'
                role='combobox'
                aria-expanded={true}
                aria-controls={LISTBOX_ID}
                aria-activedescendant={active >= 0 ? optionId(active) : undefined}
                aria-autocomplete='list'
                value={query}
                placeholder={placeholder}
                aria-label={placeholder}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={onInputKeyDown}
            />
        </div>
    );

    return (
        <GenericModal
            title={title}
            ariaLabel={title}
            className={styles.root}
            initialFocus={inputRef}
            headerContent={searchField}
            onClose={onClose}
        >
            <div
                className={styles.body}
                id={LISTBOX_ID}
                role='listbox'
                aria-label={title}
            >
                {total === 0 ? (
                    <div className={styles.empty}>
                        {formatMessage({id: 'docs.switcher.noResults', defaultMessage: 'No spaces or pages found'})}
                    </div>
                ) : groups.map((group) => (group.entries.length === 0 ? null : (
                    <div
                        key={group.id}
                        className={styles.group}
                        role='group'
                        aria-label={group.title}
                    >
                        <div className={styles.groupTitle}>{group.title}</div>
                        {group.entries.map(renderEntry)}
                    </div>
                )))}
            </div>
        </GenericModal>
    );
};

export default DocsSwitcher;

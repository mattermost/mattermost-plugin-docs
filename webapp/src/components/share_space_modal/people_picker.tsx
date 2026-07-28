// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Autocomplete} from '@base-ui-components/react/autocomplete';
import type {MemberProfile} from 'hooks/members';
import {useUserSearch} from 'hooks/user_search';
import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {Avatar} from 'webapp_globals';

import MagnifyIcon from '@mattermost/compass-icons/components/magnify';

import styles from './people_picker.module.scss';

type Props = {
    excludeIds: string[];
    onSelect: (user: MemberProfile) => void;
};

// Searchable people combobox for the share modal. Built on Base UI's Autocomplete
// (mode='none' so the list is driven by the server search, not client filtering).
// Picking a result fires onSelect and clears the query.
const PeoplePicker = ({excludeIds, onSelect}: Props) => {
    const {formatMessage} = useIntl();
    const [query, setQuery] = useState('');
    const {results, loading} = useUserSearch(query, excludeIds);

    const placeholder = formatMessage({id: 'docs.share.search', defaultMessage: 'Add people or groups'});

    const pick = (user: MemberProfile) => {
        onSelect(user);
        setQuery('');
    };

    return (
        <Autocomplete.Root
            items={results}
            mode='none'
            value={query}
            onValueChange={setQuery}
        >
            <div className={styles.control}>
                <MagnifyIcon
                    className={styles.searchIcon}
                    size={16}
                />
                <Autocomplete.Input
                    className={styles.input}
                    placeholder={placeholder}
                    aria-label={placeholder}
                />
            </div>
            <Autocomplete.Portal>
                <Autocomplete.Positioner
                    className={styles.positioner}
                    sideOffset={4}
                >
                    <Autocomplete.Popup className={styles.popup}>
                        <Autocomplete.Empty className={styles.empty}>
                            {loading ? (
                                <FormattedMessage
                                    id='docs.share.searching'
                                    defaultMessage='Searching…'
                                />
                            ) : (
                                <FormattedMessage
                                    id='docs.share.noResults'
                                    defaultMessage='No people found'
                                />
                            )}
                        </Autocomplete.Empty>
                        <Autocomplete.List>
                            {(user: MemberProfile) => (
                                <Autocomplete.Item
                                    key={user.id}
                                    value={user}
                                    className={styles.item}
                                    onClick={() => pick(user)}
                                >
                                    <Avatar
                                        url={user.avatarUrl}
                                        username={user.username}
                                        size='sm'
                                        name=''
                                    />
                                    <span className={styles.itemInfo}>
                                        <span className={styles.itemName}>{user.displayName}</span>
                                        {user.username && (
                                            <span className={styles.itemUsername}>
                                                <FormattedMessage
                                                    id='docs.share.handle'
                                                    defaultMessage='@{username}'
                                                    values={{username: user.username}}
                                                />
                                            </span>
                                        )}
                                    </span>
                                </Autocomplete.Item>
                            )}
                        </Autocomplete.List>
                    </Autocomplete.Popup>
                </Autocomplete.Positioner>
            </Autocomplete.Portal>
        </Autocomplete.Root>
    );
};

export default PeoplePicker;

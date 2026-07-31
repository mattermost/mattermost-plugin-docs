// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Combobox} from '@base-ui-components/react/combobox';
import type {MemberProfile} from 'hooks/members';
import {useUserSearch} from 'hooks/user_search';
import React, {useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {Avatar} from 'webapp_globals';

import CloseIcon from '@mattermost/compass-icons/components/close';
import MagnifyIcon from '@mattermost/compass-icons/components/magnify';

import styles from './people_picker.module.scss';

type Props = {
    selected: MemberProfile[];
    excludeIds: string[];
    onChange: (users: MemberProfile[]) => void;
};

const itemToLabel = (user: MemberProfile) => user.displayName;
const sameUser = (a: MemberProfile, b: MemberProfile) => a.id === b.id;

// Multi-select people picker for the share modal: chips for who's been picked,
// plus a server-driven suggestion list.
//
// Base UI's Combobox (not Autocomplete) is the right primitive here — it owns the
// selected values and ships the Chips/Chip/ChipRemove parts. `filter={null}`
// disables client-side filtering because `useUserSearch` already queries the
// server, and `itemToStringLabel` keeps Base UI from serialising a profile object
// when it needs a string for an item.
const PeoplePicker = ({selected, excludeIds, onChange}: Props) => {
    const {formatMessage} = useIntl();
    const [query, setQuery] = useState('');
    const {results, loading} = useUserSearch(query, excludeIds);

    const placeholder = formatMessage({id: 'docs.share.search', defaultMessage: 'Add people or groups'});

    return (
        <Combobox.Root<MemberProfile, true>
            multiple={true}
            items={results}
            filter={null}
            value={selected}
            onValueChange={onChange}
            inputValue={query}
            onInputValueChange={setQuery}
            itemToStringLabel={itemToLabel}
            isItemEqualToValue={sameUser}
            openOnInputClick={false}
        >
            <Combobox.Chips className={styles.control}>
                <MagnifyIcon
                    className={styles.searchIcon}
                    size={16}
                />
                {selected.map((user) => (
                    <Combobox.Chip
                        key={user.id}
                        className={styles.chip}
                    >
                        <Avatar
                            url={user.avatarUrl}
                            username={user.username}
                            size='xs'
                            name=''
                        />
                        <span className={styles.chipName}>{user.displayName}</span>
                        <Combobox.ChipRemove
                            className={styles.chipRemove}
                            aria-label={formatMessage(
                                {id: 'docs.share.remove', defaultMessage: 'Remove {name}'},
                                {name: user.displayName},
                            )}
                        >
                            <CloseIcon size={12}/>
                        </Combobox.ChipRemove>
                    </Combobox.Chip>
                ))}
                <Combobox.Input
                    className={styles.input}
                    placeholder={selected.length === 0 ? placeholder : undefined}
                    aria-label={placeholder}
                />
            </Combobox.Chips>
            <Combobox.Portal>
                <Combobox.Positioner
                    className={styles.positioner}
                    sideOffset={4}
                >
                    <Combobox.Popup className={styles.popup}>
                        <Combobox.Empty className={styles.empty}>
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
                        </Combobox.Empty>
                        <Combobox.List className={styles.list}>
                            {(user: MemberProfile) => (
                                <Combobox.Item
                                    key={user.id}
                                    value={user}
                                    className={styles.item}
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
                                </Combobox.Item>
                            )}
                        </Combobox.List>
                    </Combobox.Popup>
                </Combobox.Positioner>
            </Combobox.Portal>
        </Combobox.Root>
    );
};

export default PeoplePicker;

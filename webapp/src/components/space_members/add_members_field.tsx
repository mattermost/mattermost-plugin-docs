// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {MemberProfile} from 'hooks/members';
import React, {useCallback, useMemo, useState} from 'react';
import {FormattedMessage} from 'react-intl';

import {PrimaryButton} from 'components/form_controls/button';

import PeoplePicker from './people_picker';
import styles from './space_members.module.scss';

type Props = {

    /** Members already in the space. Pending selections are excluded on top of these. */
    excludeIds: string[];

    /** Resolves to the users that failed, which stay as chips. */
    onAdd: (users: MemberProfile[]) => Promise<MemberProfile[]>;
    disabled: boolean;

    /** 48px field, matching the Share modal search. */
    large?: boolean;

    /**
     * Commit each selection immediately instead of collecting chips behind Add.
     * The Share modal uses this; Settings keeps the explicit Add button.
     */
    commitOnSelect?: boolean;
};

/**
 * The add-people control. Settings collects chips then commits with Add; the
 * Share modal commits each pick immediately (no Add button).
 *
 * Owns the pending selection so consumers pass only the current member ids and never
 * have to know that pending chips exist.
 */
const AddMembersField = ({excludeIds, onAdd, disabled, large = false, commitOnSelect = false}: Props) => {
    const [pending, setPending] = useState<MemberProfile[]>([]);
    const [adding, setAdding] = useState(false);
    const busy = disabled || adding;

    const exclude = useMemo(
        () => [...excludeIds, ...pending.map((user) => user.id)],
        [excludeIds, pending],
    );

    const add = useCallback(async (users: MemberProfile[]) => {
        setAdding(true);
        try {
            setPending(await onAdd(users));
        } finally {
            setAdding(false);
        }
    }, [onAdd]);

    const changePending = useCallback((users: MemberProfile[]) => {
        setPending(users);
        if (commitOnSelect && users.length > 0 && !busy) {
            void add(users);
        }
    }, [add, busy, commitOnSelect]);

    return (
        <div className={styles.addField}>
            <PeoplePicker
                selected={pending}
                excludeIds={exclude}
                onChange={changePending}
                disabled={busy}
                large={large}
            />
            {!commitOnSelect && (
                <PrimaryButton
                    type='button'
                    size='sm'
                    disabled={pending.length === 0 || busy}
                    onClick={() => add(pending)}
                >
                    <FormattedMessage
                        id='docs.spaceMembers.add'
                        defaultMessage='Add'
                    />
                </PrimaryButton>
            )}
        </div>
    );
};

export default AddMembersField;

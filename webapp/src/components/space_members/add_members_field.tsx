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
};

/**
 * The add-people control: pick several, then commit them with Add.
 *
 * Owns the pending selection so consumers pass only the current member ids and never
 * have to know that pending chips exist.
 */
const AddMembersField = ({excludeIds, onAdd, disabled}: Props) => {
    const [pending, setPending] = useState<MemberProfile[]>([]);
    const [adding, setAdding] = useState(false);
    const busy = disabled || adding;

    const exclude = useMemo(
        () => [...excludeIds, ...pending.map((user) => user.id)],
        [excludeIds, pending],
    );

    const add = useCallback(async () => {
        setAdding(true);
        try {
            setPending(await onAdd(pending));
        } finally {
            setAdding(false);
        }
    }, [onAdd, pending]);

    return (
        <div className={styles.addField}>
            <PeoplePicker
                selected={pending}
                excludeIds={exclude}
                onChange={setPending}
                disabled={busy}
            />
            <PrimaryButton
                type='button'
                size='sm'
                disabled={pending.length === 0 || busy}
                onClick={add}
            >
                <FormattedMessage
                    id='docs.spaceMembers.add'
                    defaultMessage='Add'
                />
            </PrimaryButton>
        </div>
    );
};

export default AddMembersField;

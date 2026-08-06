// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {RestError} from 'client/rest';
import {
    getMemberProfiles,
    getSpaceAccess,
    getSpaceMembers,
    setDefaultCapabilities,
    setMemberCapabilities,
} from 'client/space_permissions';
import {useAppSelector} from 'hooks/redux';
import React, {useCallback, useEffect, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import type {UserProfile} from '@mattermost/types/users';

import {getTeammateNameDisplaySetting} from 'mattermost-redux/selectors/entities/preferences';
import {displayUsername} from 'mattermost-redux/utils/user_utils';

import {PrimaryButton, TertiaryButton} from 'components/form-controls/button';
import GenericModal from 'components/generic_modal/generic_modal';

import type {Space} from 'types/docs';
import type {Capability, SpaceMember} from 'types/permissions';
import {Capabilities, DEFAULT_CAPABILITY_ORDER, MEMBER_CAPABILITY_ORDER} from 'types/permissions';

import CapabilityToggles, {useCapabilityLabels} from './capability_toggles';
import styles from './space_settings_modal.module.scss';

type Props = {
    space: Space;
    onClose: () => void;
};

// One page of members. The list is a management surface for a space's own
// membership, not a directory, so a single generous page covers it; has_more
// drives an explicit notice rather than silently truncating.
const MEMBERS_PER_PAGE = 100;

const sameSet = (a: Capability[], b: Capability[]): boolean =>
    a.length === b.length && a.every((capability) => b.includes(capability));

const SpaceSettingsModal = ({space, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const capabilityLabels = useCapabilityLabels();

    const teammateNameDisplay = useAppSelector(getTeammateNameDisplaySetting) || '';

    // Held locally rather than read from the host's user entities: the modal
    // needs names for exactly the members it lists, and a space's membership is
    // not otherwise loaded into that store.
    const [profiles, setProfiles] = useState<Record<string, UserProfile>>({});

    const [loading, setLoading] = useState(true);
    const [error, setError] = useState('');

    const [defaults, setDefaults] = useState<Capability[]>([]);
    const [savedDefaults, setSavedDefaults] = useState<Capability[]>([]);
    const [savingDefaults, setSavingDefaults] = useState(false);

    const [members, setMembers] = useState<SpaceMember[]>([]);
    const [hasMoreMembers, setHasMoreMembers] = useState(false);
    const [savingMember, setSavingMember] = useState('');

    // Whether the caller may edit the space-wide default. Manage authority is
    // enough to reach this screen and change a member's grants, but repointing
    // the default is space-admin only, so the section renders read-only for a
    // manage-tier caller rather than failing on save.
    const [canEditDefaults, setCanEditDefaults] = useState(false);

    // Members may be unreadable while the space itself is readable: the listing
    // requires manage authority. That is a narrower view, not an error.
    const [canListMembers, setCanListMembers] = useState(false);

    const describeError = useCallback((err: unknown): string => {
        if (err instanceof RestError) {
            return err.message;
        }
        return formatMessage({id: 'docs.spaceSettings.genericError', defaultMessage: 'Something went wrong. Please try again.'});
    }, [formatMessage]);

    useEffect(() => {
        let cancelled = false;

        const load = async () => {
            setLoading(true);
            setError('');
            try {
                const access = await getSpaceAccess(space.id);
                if (cancelled) {
                    return;
                }
                setDefaults(access.default_capabilities);
                setSavedDefaults(access.default_capabilities);
                setCanEditDefaults(access.capabilities.includes(Capabilities.ADMIN_SPACE));

                try {
                    const page = await getSpaceMembers(space.id, 0, MEMBERS_PER_PAGE);
                    if (cancelled) {
                        return;
                    }
                    setMembers(page.items);
                    setHasMoreMembers(page.has_more);
                    setCanListMembers(true);

                    // Names are cosmetic: a failure here leaves the rows
                    // labelled by user id rather than failing the screen.
                    const fetched = await getMemberProfiles(page.items.map((member) => member.user_id)).catch(() => []);
                    if (!cancelled) {
                        setProfiles(Object.fromEntries(fetched.map((profile) => [profile.id, profile])));
                    }
                } catch (membersErr) {
                    if (cancelled) {
                        return;
                    }

                    // A refusal here means the caller can read the space but not
                    // manage it; anything else is a real failure worth showing.
                    if (membersErr instanceof RestError && membersErr.status === 403) {
                        setCanListMembers(false);
                    } else {
                        setError(describeError(membersErr));
                    }
                }
            } catch (accessErr) {
                if (!cancelled) {
                    setError(describeError(accessErr));
                }
            } finally {
                if (!cancelled) {
                    setLoading(false);
                }
            }
        };

        load();
        return () => {
            cancelled = true;
        };
    }, [space.id, describeError]);

    const saveDefaults = async () => {
        setSavingDefaults(true);
        setError('');
        try {
            const updated = await setDefaultCapabilities(space.id, defaults);
            setDefaults(updated.default_capabilities);
            setSavedDefaults(updated.default_capabilities);
        } catch (err) {
            setError(describeError(err));
        } finally {
            setSavingDefaults(false);
        }
    };

    const saveMember = async (userId: string, granted: Capability[]) => {
        setSavingMember(userId);
        setError('');
        try {
            const updated = await setMemberCapabilities(space.id, userId, granted);
            setMembers((current) => current.map((member) => (member.user_id === userId ? updated : member)));
        } catch (err) {
            setError(describeError(err));
        } finally {
            setSavingMember('');
        }
    };

    // useFallbackUsername is off so an unresolved profile yields an empty string
    // rather than the generic "Someone": on a permissions screen the row has to
    // stay attributable, so it falls back to the user id instead.
    const memberName = (userId: string): string => displayUsername(profiles[userId], teammateNameDisplay, false) || userId;

    const footer = (
        <TertiaryButton
            type='button'
            onClick={onClose}
        >
            {formatMessage({id: 'docs.spaceSettings.close', defaultMessage: 'Close'})}
        </TertiaryButton>
    );

    return (
        <GenericModal
            className={styles.modal}
            title={formatMessage({id: 'docs.spaceSettings.title', defaultMessage: 'Permissions for {name}'}, {name: space.title})}
            onClose={onClose}
            footer={footer}
        >
            <div className={styles.body}>
                {error !== '' && (
                    <div
                        className={styles.error}
                        role='alert'
                    >
                        {error}
                    </div>
                )}

                {loading ? (
                    <p className={styles.note}>
                        <FormattedMessage
                            id='docs.spaceSettings.loading'
                            defaultMessage='Loading permissions…'
                        />
                    </p>
                ) : (
                    <>
                        <section className={styles.section}>
                            <h2 className={styles.sectionTitle}>
                                <FormattedMessage
                                    id='docs.spaceSettings.defaults.title'
                                    defaultMessage='Everyone in this space'
                                />
                            </h2>
                            <p className={styles.note}>
                                <FormattedMessage
                                    id='docs.spaceSettings.defaults.description'
                                    defaultMessage='What every member can do without an individual grant. {readPage} is always included.'
                                    values={{readPage: capabilityLabels[Capabilities.READ_PAGE]}}
                                />
                            </p>
                            <CapabilityToggles
                                idPrefix='docs-space-default'
                                legend={formatMessage({id: 'docs.spaceSettings.defaults.legend', defaultMessage: 'Default permissions'})}
                                options={DEFAULT_CAPABILITY_ORDER}
                                selected={defaults}
                                disabled={!canEditDefaults || savingDefaults}
                                onChange={setDefaults}
                            />
                            {canEditDefaults ? (
                                <PrimaryButton
                                    type='button'
                                    disabled={savingDefaults || sameSet(defaults, savedDefaults)}
                                    onClick={saveDefaults}
                                >
                                    {formatMessage({id: 'docs.spaceSettings.defaults.save', defaultMessage: 'Save defaults'})}
                                </PrimaryButton>
                            ) : (
                                <p className={styles.note}>
                                    <FormattedMessage
                                        id='docs.spaceSettings.defaults.readOnly'
                                        defaultMessage='Only a space administrator can change the default permissions.'
                                    />
                                </p>
                            )}
                        </section>

                        {canListMembers && (
                            <section className={styles.section}>
                                <h2 className={styles.sectionTitle}>
                                    <FormattedMessage
                                        id='docs.spaceSettings.members.title'
                                        defaultMessage='Individual members'
                                    />
                                </h2>
                                <p className={styles.note}>
                                    <FormattedMessage
                                        id='docs.spaceSettings.members.description'
                                        defaultMessage='Grants added on top of the space default. Clearing them leaves the member with the default.'
                                    />
                                </p>

                                {members.map((member) => (
                                    <div
                                        key={member.user_id}
                                        className={styles.member}
                                    >
                                        <div className={styles.memberName}>{memberName(member.user_id)}</div>
                                        {member.is_guest ? (
                                            <p className={styles.note}>
                                                <FormattedMessage
                                                    id='docs.spaceSettings.members.guest'
                                                    defaultMessage='Guests can only view pages, and cannot be granted more.'
                                                />
                                            </p>
                                        ) : (
                                            <CapabilityToggles
                                                idPrefix={`docs-space-member-${member.user_id}`}
                                                legend={formatMessage(
                                                    {id: 'docs.spaceSettings.members.legend', defaultMessage: 'Permissions for {name}'},
                                                    {name: memberName(member.user_id)},
                                                )}
                                                options={MEMBER_CAPABILITY_ORDER}
                                                selected={member.granted_capabilities}
                                                disabled={savingMember === member.user_id}
                                                onChange={(next) => saveMember(member.user_id, next)}
                                            />
                                        )}
                                    </div>
                                ))}

                                {hasMoreMembers && (
                                    <p className={styles.note}>
                                        <FormattedMessage
                                            id='docs.spaceSettings.members.truncated'
                                            defaultMessage='Only the first {count} members are shown.'
                                            values={{count: MEMBERS_PER_PAGE}}
                                        />
                                    </p>
                                )}
                            </section>
                        )}
                    </>
                )}
            </div>
        </GenericModal>
    );
};

export default SpaceSettingsModal;

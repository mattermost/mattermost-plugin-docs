// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSpaceMemberProfiles} from 'hooks/members';
import {useSpaceStats} from 'hooks/spaces';
import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';
import {Avatar, Timestamp} from 'webapp_globals';
import type {TimestampUnit} from 'webapp_globals';

import CloseIcon from '@mattermost/compass-icons/components/close';

import {Button} from 'components/form_controls/button';

import type {Space} from 'types/docs';

import styles from './space_info_panel.module.scss';

// Relative "Created …" buckets for the host Timestamp, mirroring the page bar's
// relative spec but reaching back far enough for an old space.
const CREATED_TIME_SPEC: TimestampUnit[] = [
    ['minute', -59],
    ['hour', -48],
    ['day', -30],
    ['month', -12],
    'year',
];

type Props = {
    space: Space;
    onClose: () => void;
};

// Read-only right-hand panel mirroring core's Channel Info RHS: a header with a
// close control over sections for the space identity, its description, its
// members, and a small meta area. Editing lives in Space Settings, so nothing
// here mutates the space.
const SpaceInfoPanel = ({space, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const {pageCount, memberCount} = useSpaceStats(space.id);
    const members = useSpaceMemberProfiles(space.id);

    const closeLabel = formatMessage({id: 'docs.spaceInfo.close', defaultMessage: 'Close info'});

    // Timestamp's `style` is a narrow/short/long format variant, not a DOM style object.
    /* eslint-disable react/style-prop-object */
    const createdRelative = Timestamp ? (
        <Timestamp
            value={space.create_at}
            units={CREATED_TIME_SPEC}
            useTime={false}
            style='long'
        />
    ) : null;
    /* eslint-enable react/style-prop-object */

    return (
        <aside
            className={styles.panel}
            aria-label={formatMessage({id: 'docs.spaceInfo.title', defaultMessage: 'Space info'})}
        >
            <div className={styles.header}>
                <h2 className={styles.headerTitle}>
                    <FormattedMessage
                        id='docs.spaceInfo.title'
                        defaultMessage='Space info'
                    />
                </h2>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    className='btn-icon'
                    aria-label={closeLabel}
                    onClick={onClose}
                >
                    <CloseIcon size={18}/>
                </Button>
            </div>

            <div className={styles.body}>
                <div className={styles.identity}>
                    <span
                        className={styles.icon}
                        aria-hidden={true}
                    >
                        <SpaceIcon
                            space={space}
                            size={40}
                        />
                    </span>
                    <span className={styles.spaceTitle}>{space.title}</span>
                </div>

                <section className={styles.section}>
                    <h3 className={styles.sectionTitle}>
                        <FormattedMessage
                            id='docs.spaceInfo.description'
                            defaultMessage='Description'
                        />
                    </h3>
                    {space.description ? (
                        <p className={styles.description}>{space.description}</p>
                    ) : (
                        <p className={styles.placeholder}>
                            <FormattedMessage
                                id='docs.spaceInfo.descriptionPlaceholder'
                                defaultMessage='Add a space description'
                            />
                        </p>
                    )}
                </section>

                <section className={styles.section}>
                    <h3 className={styles.sectionTitle}>
                        <FormattedMessage
                            id='docs.spaceInfo.members'
                            defaultMessage='Members'
                        />
                        <span className={styles.count}>{memberCount}</span>
                    </h3>
                    <div className={styles.memberList}>
                        {members.map((member) => (
                            <div
                                key={member.id}
                                className={styles.memberRow}
                            >
                                <Avatar
                                    url={member.avatarUrl}
                                    username={member.username}
                                    size='sm'
                                    name=''
                                />
                                <span className={styles.memberInfo}>
                                    <span className={styles.memberName}>{member.displayName}</span>
                                    {member.username && (
                                        <span className={styles.memberUsername}>
                                            <FormattedMessage
                                                id='docs.spaceInfo.handle'
                                                defaultMessage='@{username}'
                                                values={{username: member.username}}
                                            />
                                        </span>
                                    )}
                                </span>
                            </div>
                        ))}
                    </div>
                </section>

                <section className={styles.section}>
                    <dl className={styles.meta}>
                        <div className={styles.metaRow}>
                            <dt className={styles.metaLabel}>
                                <FormattedMessage
                                    id='docs.spaceInfo.pages'
                                    defaultMessage='Pages'
                                />
                            </dt>
                            <dd className={styles.metaValue}>{pageCount}</dd>
                        </div>
                        {createdRelative && (
                            <div className={styles.metaRow}>
                                <dt className={styles.metaLabel}>
                                    <FormattedMessage
                                        id='docs.spaceInfo.created'
                                        defaultMessage='Created'
                                    />
                                </dt>
                                <dd className={styles.metaValue}>{createdRelative}</dd>
                            </div>
                        )}
                    </dl>
                </section>
            </div>
        </aside>
    );
};

export default SpaceInfoPanel;

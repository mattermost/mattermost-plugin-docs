// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useSpaceMemberProfiles} from 'hooks/members';
import {useAppDispatch} from 'hooks/redux';
import {useSpaceStats} from 'hooks/spaces';
import React, {useCallback} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';
import {SPACE_DESCRIPTION_MAX_LENGTH} from 'validation/space_schema';
import {Timestamp} from 'webapp_globals';
import type {TimestampUnit} from 'webapp_globals';

import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';

import {updateSpace} from 'store/actions';
import {useSpacePermissions} from 'store/permissions';

import BasicInputModal from 'components/basic_input_modal/basic_input_modal';
import {Button} from 'components/form_controls/button';
import {openDocsModal} from 'components/modals';
import RhsPanel from 'components/rhs/rhs_panel';

import type {Space} from 'types/docs';

import SpaceInfoMembers from './space_info_members';
import SpaceInfoMenu from './space_info_menu';
import styles from './space_info_panel.module.scss';

// Relative "Created …" buckets for the host Timestamp, mirroring the page header's
// relative spec but reaching back far enough for an old space.
const CREATED_TIME_SPEC: TimestampUnit[] = [
    ['minute', -59],
    ['hour', -48],
    ['day', -30],
    ['month', -12],
    'year',
];

// The panel's navigation: the root screen, or a drilled-into sub-panel. Owned by
// the space view, so other chrome (the header's members controls) can open the
// panel straight onto a sub-panel.
export type SpaceInfoView = 'root' | 'members';

type Props = {
    space: Space;
    view: SpaceInfoView;
    onViewChange: (view: SpaceInfoView) => void;
    onClose: () => void;
};

// The Space Info RHS, mirroring core's Channel Info: the space identity, its
// inline-editable description, an action menu, the member list, and a small meta
// area — or, drilled in, the full member list on its own. The description is the
// only field editable from here; everything else is read-only and lives in Space
// Settings.
const SpaceInfoPanel = ({space, view, onViewChange, onClose}: Props) => {
    const {formatMessage} = useIntl();
    const {pageCount, memberCount} = useSpaceStats(space.id);
    const members = useSpaceMemberProfiles(space.id);
    const dispatch = useAppDispatch();
    const {canManageMembers} = useSpacePermissions(space.id);

    const editDescriptionLabel = formatMessage({id: 'docs.spaceInfo.editDescription', defaultMessage: 'Edit description'});

    // The description is the one field editable from here; everything else lives
    // in Space Settings. An empty value clears it, so the modal allows empty.
    const editDescription = useCallback(() => {
        openDocsModal((modal) => (
            <BasicInputModal
                title={editDescriptionLabel}
                label={formatMessage({id: 'docs.spaceInfo.descriptionLabel', defaultMessage: 'Space description'})}
                initialValue={space.description ?? ''}
                maxLength={SPACE_DESCRIPTION_MAX_LENGTH}
                multiline={true}
                allowEmpty={true}
                onConfirm={async (description) => {
                    await dispatch(updateSpace(space.id, {description}));
                }}
                onClose={modal.close}
            />
        ));
    }, [dispatch, editDescriptionLabel, formatMessage, space.description, space.id]);

    // Timestamp's `style` is a narrow/short/long format variant, not a DOM style object.
    /* eslint-disable react/style-prop-object */
    const createdRelative = (
        <Timestamp
            value={space.create_at}
            units={CREATED_TIME_SPEC}
            useTime={false}
            style='long'
        />
    );
    /* eslint-enable react/style-prop-object */

    return (
        <RhsPanel
            name={formatMessage({id: 'docs.spaceInfo.title', defaultMessage: 'Space info'})}
            title={view === 'members' ? (
                <FormattedMessage
                    id='docs.spaceInfo.members'
                    defaultMessage='Members'
                />
            ) : undefined}
            widthKey='spaceInfo'
            onBack={view === 'root' ? undefined : () => onViewChange('root')}
            onClose={onClose}
        >
            {view === 'members' && <SpaceInfoMembers members={members}/>}

            {view === 'root' && (
                <>
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
                        {canManageMembers ? (

                        // The whole description is the edit target, with a pencil
                        // revealed on hover — core's channel-info EditableArea.
                            <Button
                                emphasis='quaternary'
                                size='sm'
                                className={styles.editableArea}
                                aria-label={editDescriptionLabel}
                                onClick={editDescription}
                            >
                                <span className={classNames(styles.editableText, {[styles.editablePlaceholder]: !space.description})}>
                                    {space.description || formatMessage({id: 'docs.spaceInfo.descriptionPlaceholder', defaultMessage: 'Add a space description'})}
                                </span>
                                <span
                                    className={styles.editIcon}
                                    aria-hidden={true}
                                >
                                    <PencilOutlineIcon size={14}/>
                                </span>
                            </Button>
                        ) : (
                            <p className={space.description ? styles.description : styles.placeholder}>
                                {space.description || formatMessage({id: 'docs.spaceInfo.noDescription', defaultMessage: 'No description'})}
                            </p>
                        )}
                    </section>

                    <SpaceInfoMenu
                        space={space}
                        memberCount={memberCount}
                        onShowMembers={() => onViewChange('members')}
                    />

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
                            <div className={styles.metaRow}>
                                <dt className={styles.metaLabel}>
                                    <FormattedMessage
                                        id='docs.spaceInfo.created'
                                        defaultMessage='Created'
                                    />
                                </dt>
                                <dd className={styles.metaValue}>{createdRelative}</dd>
                            </div>
                        </dl>
                    </section>
                </>
            )}
        </RhsPanel>
    );
};

export default SpaceInfoPanel;

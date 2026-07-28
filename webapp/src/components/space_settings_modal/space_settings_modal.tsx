// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import {useSpaceMemberProfiles} from 'hooks/members';
import {useDocsNavigation} from 'hooks/navigation';
import {useAppDispatch} from 'hooks/redux';
import React, {useMemo, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';
import {Avatar} from 'webapp_globals';

import ArchiveOutlineIcon from '@mattermost/compass-icons/components/archive-outline';
import ChevronDownIcon from '@mattermost/compass-icons/components/chevron-down';
import CogOutlineIcon from '@mattermost/compass-icons/components/cog-outline';
import GlobeIcon from '@mattermost/compass-icons/components/globe';
import InformationOutlineIcon from '@mattermost/compass-icons/components/information-outline';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';
import type IconProps from '@mattermost/compass-icons/components/props';
import ShieldOutlineIcon from '@mattermost/compass-icons/components/shield-outline';

import {deleteSpace, updateSpace} from 'store/actions';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import {Button, DestructiveButton, PrimaryButton, TertiaryButton} from 'components/form_controls/button';
import PublicPrivateSelector from 'components/form_controls/public_private_selector';
import TextArea from 'components/form_controls/text_area';
import TextInput from 'components/form_controls/text_input';
import GenericModal from 'components/generic_modal/generic_modal';

import type {Space} from 'types/docs';

import styles from './space_settings_modal.module.scss';

export type SpaceSettingsTab = 'info' | 'permissions' | 'configuration' | 'archive';

type Props = {
    space: Space;
    onClose: () => void;
    initialTab?: SpaceSettingsTab;
};

type TabDef = {
    id: SpaceSettingsTab;
    label: string;
    icon: React.ComponentType<IconProps>;
    destructive?: boolean;
};

const SpaceSettingsModal = ({space, onClose, initialTab = 'info'}: Props) => {
    const {formatMessage} = useIntl();
    const {paths} = useDocsNavigation();
    const [activeTab, setActiveTab] = useState<SpaceSettingsTab>(initialTab);

    const tabs: TabDef[] = [
        {id: 'info', label: formatMessage({id: 'docs.spaceSettings.tab.info', defaultMessage: 'Info'}), icon: InformationOutlineIcon},
        {id: 'permissions', label: formatMessage({id: 'docs.spaceSettings.tab.permissions', defaultMessage: 'Permissions'}), icon: ShieldOutlineIcon},
        {id: 'configuration', label: formatMessage({id: 'docs.spaceSettings.tab.configuration', defaultMessage: 'Configuration'}), icon: CogOutlineIcon},
        {id: 'archive', label: formatMessage({id: 'docs.spaceSettings.tab.archive', defaultMessage: 'Archive space'}), icon: ArchiveOutlineIcon, destructive: true},
    ];

    const info = useInfoTab(space, onClose);

    const footer = activeTab === 'info' ? (
        <>
            <TertiaryButton
                type='button'
                onClick={onClose}
            >
                <FormattedMessage
                    id='docs.spaceSettings.cancel'
                    defaultMessage='Cancel'
                />
            </TertiaryButton>
            <PrimaryButton
                type='button'
                disabled={!info.canSave}
                onClick={info.save}
            >
                <FormattedMessage
                    id='docs.spaceSettings.save'
                    defaultMessage='Save'
                />
            </PrimaryButton>
        </>
    ) : undefined;

    const subtitle = (
        <span className={styles.subtitle}>
            <span
                className={styles.subtitleIcon}
                aria-hidden={true}
            >
                <SpaceIcon
                    space={space}
                    size={16}
                />
            </span>
            {space.title}
        </span>
    );

    return (
        <GenericModal
            className={styles.modal}
            title={formatMessage({id: 'docs.spaceSettings.title', defaultMessage: 'Space Settings'})}
            ariaLabel={formatMessage({id: 'docs.spaceSettings.title', defaultMessage: 'Space Settings'})}
            headerContent={subtitle}
            onClose={onClose}
            footer={footer}
        >
            <div className={styles.body}>
                <nav
                    className={styles.nav}
                    aria-label={formatMessage({id: 'docs.spaceSettings.navLabel', defaultMessage: 'Space settings sections'})}
                >
                    {tabs.map((tab) => {
                        const Icon = tab.icon;
                        const active = tab.id === activeTab;
                        return (
                            <Button
                                key={tab.id}
                                type='button'
                                emphasis='quaternary'
                                size='sm'
                                aria-current={active}
                                className={classNames(styles.navItem, {
                                    [styles.navItemActive]: active,
                                    [styles.navItemDestructive]: tab.destructive && !active,
                                })}
                                onClick={() => setActiveTab(tab.id)}
                            >
                                <Icon size={16}/>
                                <span className={styles.navLabel}>{tab.label}</span>
                            </Button>
                        );
                    })}
                </nav>

                <div
                    className={styles.pane}
                    role='tabpanel'
                >
                    {activeTab === 'info' && (
                        <InfoTab
                            space={space}
                            info={info}
                            url={`${window.location.origin}${paths.space(space.id)}`}
                        />
                    )}
                    {activeTab === 'permissions' && <PermissionsTab space={space}/>}
                    {activeTab === 'configuration' && <ConfigurationTab/>}
                    {activeTab === 'archive' && (
                        <ArchiveTab
                            space={space}
                            onClose={onClose}
                        />
                    )}
                </div>
            </div>
        </GenericModal>
    );
};

type InfoTabState = {
    name: string;
    setName: (value: string) => void;
    description: string;
    setDescription: (value: string) => void;
    error?: string;
    canSave: boolean;
    save: () => void;
};

// Owns the editable Info-tab fields and the save flow so the footer (rendered by
// the modal shell) and the pane share one source of truth.
function useInfoTab(space: Space, onClose: () => void): InfoTabState {
    const dispatch = useAppDispatch();
    const [name, setName] = useState(space.title);
    const [description, setDescription] = useState(space.description ?? '');
    const [error, setError] = useState<string>();
    const [saving, setSaving] = useState(false);

    const dirty = name.trim() !== space.title || description.trim() !== (space.description ?? '');
    const canSave = dirty && Boolean(name.trim()) && !saving;

    const save = async () => {
        if (!canSave) {
            return;
        }
        setSaving(true);
        setError(undefined);
        try {
            await dispatch(updateSpace(space.id, {title: name.trim(), description: description.trim()}));
            onClose();
        } catch (err) {
            setSaving(false);
            setError(err instanceof Error ? err.message : String(err));
        }
    };

    return {name, setName, description, setDescription, error, canSave, save};
}

const InfoTab = ({space, info, url}: {space: Space; info: InfoTabState; url: string}) => {
    const {formatMessage} = useIntl();

    return (
        <>
            <h2 className={styles.heading}>
                <FormattedMessage
                    id='docs.spaceSettings.info.heading'
                    defaultMessage='Space info'
                />
            </h2>

            <TextInput
                id='docs-space-settings-name'
                label={formatMessage({id: 'docs.spaceSettings.info.nameLabel', defaultMessage: 'Space name'})}
                value={info.name}
                onChange={info.setName}
                maxLength={64}
                leading={(
                    <SpaceIcon
                        space={space}
                        size={18}
                    />
                )}
            />

            <div className={styles.fieldLabel}>
                <FormattedMessage
                    id='docs.spaceSettings.info.urlLabel'
                    defaultMessage='Space URL'
                />
            </div>
            <div className={styles.urlRow}>
                <span className={styles.urlText}>{url}</span>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    disabled={true}
                    aria-disabled={true}
                >
                    <FormattedMessage
                        id='docs.spaceSettings.info.editUrl'
                        defaultMessage='Edit'
                    />
                </Button>
            </div>

            <div className={styles.fieldLabel}>
                <FormattedMessage
                    id='docs.spaceSettings.info.descriptionLabel'
                    defaultMessage='Description'
                />
            </div>
            <TextArea
                id='docs-space-settings-description'
                label={formatMessage({id: 'docs.spaceSettings.info.descriptionPlaceholder', defaultMessage: 'Enter a description for this space…'})}
                value={info.description}
                onChange={info.setDescription}
                maxLength={1024}
                rows={3}
            />
            <div className={styles.helper}>
                <FormattedMessage
                    id='docs.spaceSettings.info.descriptionHelper'
                    defaultMessage='Describe how this space should be used. This will show when browsing spaces.'
                />
            </div>

            <div className={styles.fieldLabel}>
                <FormattedMessage
                    id='docs.spaceSettings.info.landingLabel'
                    defaultMessage='Default landing page'
                />
            </div>
            <Button
                type='button'
                emphasis='quaternary'
                size='sm'
                disabled={true}
                aria-disabled={true}
                className={styles.selectStub}
            >
                <span className={styles.selectStubLabel}>
                    <SpaceIcon
                        space={space}
                        size={16}
                    />
                    {space.title}
                </span>
                <ChevronDownIcon size={16}/>
            </Button>

            {info.error && <div className={styles.error}>{info.error}</div>}
        </>
    );
};

const PermissionsTab = ({space}: {space: Space}) => {
    const {formatMessage} = useIntl();
    const members = useSpaceMemberProfiles(space.id);

    // Scaffolding: view-access and per-member capabilities land with PR #10, so
    // the selector, search and role dropdowns are visual only.
    const accessOptions = useMemo(() => [
        {
            value: 'public',
            icon: <GlobeIcon size={20}/>,
            title: formatMessage({id: 'docs.spaceSettings.permissions.public.title', defaultMessage: 'Public'}),
            description: formatMessage({id: 'docs.spaceSettings.permissions.public.description', defaultMessage: 'Anyone in the team can find and view this space.'}),
        },
        {
            value: 'private',
            icon: <LockOutlineIcon size={20}/>,
            title: formatMessage({id: 'docs.spaceSettings.permissions.private.title', defaultMessage: 'Private'}),
            description: formatMessage({id: 'docs.spaceSettings.permissions.private.description', defaultMessage: 'Only invited members can view this space.'}),
            disabled: true,
            disabledReason: formatMessage({id: 'docs.spaceSettings.permissions.private.comingSoon', defaultMessage: 'Coming soon'}),
        },
    ], [formatMessage]);

    return (
        <>
            <h2 className={styles.heading}>
                <FormattedMessage
                    id='docs.spaceSettings.permissions.accessHeading'
                    defaultMessage='Space access'
                />
            </h2>
            <PublicPrivateSelector
                ariaLabel={formatMessage({id: 'docs.spaceSettings.permissions.accessLabel', defaultMessage: 'Space access'})}
                options={accessOptions}
                value='public'
                onChange={() => {}}
            />

            <h2 className={styles.heading}>
                <FormattedMessage
                    id='docs.spaceSettings.permissions.peopleHeading'
                    defaultMessage='People and groups with access'
                />
            </h2>
            <div
                className={styles.searchStub}
                aria-disabled={true}
            >
                <FormattedMessage
                    id='docs.spaceSettings.permissions.searchPlaceholder'
                    defaultMessage='Add people, groups or channels'
                />
            </div>
            <div className={styles.memberList}>
                {members.map((member) => (
                    <div
                        key={member.id}
                        className={styles.memberRow}
                    >
                        <Avatar
                            url={member.avatarUrl}
                            username={member.username}
                            size='md'
                            name=''
                        />
                        <span className={styles.memberInfo}>
                            <span className={styles.memberName}>{member.displayName}</span>
                            {member.username && (
                                <span className={styles.memberUsername}>
                                    <FormattedMessage
                                        id='docs.spaceSettings.permissions.handle'
                                        defaultMessage='@{username}'
                                        values={{username: member.username}}
                                    />
                                </span>
                            )}
                        </span>
                        <Button
                            type='button'
                            emphasis='quaternary'
                            size='sm'
                            disabled={true}
                            aria-disabled={true}
                            className={styles.roleTrigger}
                        >
                            <FormattedMessage
                                id='docs.spaceSettings.permissions.role.admin'
                                defaultMessage='Admin'
                            />
                            <ChevronDownIcon size={16}/>
                        </Button>
                    </div>
                ))}
            </div>

            <div className={styles.toggleRow}>
                <span className={styles.toggleText}>
                    <span className={styles.toggleTitle}>
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.externalSharing.title'
                            defaultMessage='External sharing'
                        />
                    </span>
                    <span className={styles.helper}>
                        <FormattedMessage
                            id='docs.spaceSettings.permissions.externalSharing.description'
                            defaultMessage='Let people outside the team access this space with a link.'
                        />
                    </span>
                </span>
                <Button
                    type='button'
                    emphasis='quaternary'
                    size='sm'
                    disabled={true}
                    aria-disabled={true}
                >
                    <FormattedMessage
                        id='docs.spaceSettings.permissions.externalSharing.comingSoon'
                        defaultMessage='Coming soon'
                    />
                </Button>
            </div>
        </>
    );
};

const ConfigurationTab = () => (
    <>
        <h2 className={styles.heading}>
            <FormattedMessage
                id='docs.spaceSettings.configuration.heading'
                defaultMessage='Configuration'
            />
        </h2>
        <p className={styles.comingSoon}>
            <FormattedMessage
                id='docs.spaceSettings.configuration.comingSoon'
                defaultMessage='Space configuration options are coming soon.'
            />
        </p>
    </>
);

const ArchiveTab = ({space, onClose}: {space: Space; onClose: () => void}) => {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();
    const {goHome} = useDocsNavigation();
    const [confirming, setConfirming] = useState(false);
    const [error, setError] = useState<string>();

    const archive = async () => {
        setError(undefined);
        try {
            await dispatch(deleteSpace(space.id));
            setConfirming(false);
            onClose();
            goHome();
        } catch (err) {
            setConfirming(false);
            setError(err instanceof Error ? err.message : String(err));
        }
    };

    return (
        <>
            <h2 className={styles.heading}>
                <FormattedMessage
                    id='docs.spaceSettings.archive.heading'
                    defaultMessage='Archive space'
                />
            </h2>
            <div className={styles.archiveCard}>
                <p className={styles.archiveCopy}>
                    <FormattedMessage
                        id='docs.spaceSettings.archive.copy'
                        defaultMessage='Archiving removes this space and its pages from the team. Members will lose access. You can ask an admin to restore it later.'
                    />
                </p>
                <DestructiveButton
                    type='button'
                    onClick={() => setConfirming(true)}
                >
                    <ArchiveOutlineIcon size={16}/>
                    <FormattedMessage
                        id='docs.spaceSettings.archive.button'
                        defaultMessage='Archive space'
                    />
                </DestructiveButton>
                {error && <div className={styles.error}>{error}</div>}
            </div>

            {confirming && (
                <ConfirmModal
                    isConfirmDestructive={true}
                    title={formatMessage({id: 'docs.spaceSettings.archive.confirmTitle', defaultMessage: 'Archive this space?'})}
                    confirmButtonText={formatMessage({id: 'docs.spaceSettings.archive.confirmButton', defaultMessage: 'Archive'})}
                    onConfirm={archive}
                    onCancel={() => setConfirming(false)}
                >
                    <FormattedMessage
                        id='docs.spaceSettings.archive.confirmBody'
                        defaultMessage='“{title}” and its pages will be archived and members will lose access. This can be undone by an admin.'
                        values={{title: space.title}}
                    />
                </ConfirmModal>
            )}
        </>
    );
};

export default SpaceSettingsModal;

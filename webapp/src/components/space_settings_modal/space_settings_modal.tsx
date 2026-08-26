// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useCanDeleteSpace} from 'hooks/permissions';
import {useAppDispatch, useAppSelector} from 'hooks/redux';
import React, {useEffect, useMemo, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';

import ArchiveOutlineIcon from '@mattermost/compass-icons/components/archive-outline';
import CogOutlineIcon from '@mattermost/compass-icons/components/cog-outline';
import FileTextOutlineIcon from '@mattermost/compass-icons/components/file-text-outline';
import HomeVariantOutlineIcon from '@mattermost/compass-icons/components/home-variant-outline';
import InformationOutlineIcon from '@mattermost/compass-icons/components/information-outline';
import type IconProps from '@mattermost/compass-icons/components/props';
import ShieldOutlineIcon from '@mattermost/compass-icons/components/shield-outline';

import {deleteSpace, fetchPages, updateSpace} from 'store/actions';
import {getPagesForSpace, getSpace} from 'store/selectors';

import ConfirmModal from 'components/confirm_modal/confirm_modal';
import {Button, DestructiveButton} from 'components/form_controls/button';
import Select from 'components/form_controls/select';
import type {SelectOption} from 'components/form_controls/select';
import TextArea from 'components/form_controls/text_area';
import TextInput from 'components/form_controls/text_input';
import GenericModal from 'components/generic_modal/generic_modal';
import SaveChangesBar from 'components/save_changes_bar/save_changes_bar';
import {Tab, TabList, TabPanel, Tabs, TabsSeparator} from 'components/tabs/tabs';

import {SPACE_PROP_DEFAULT_PAGE_ID} from 'types/docs';
import type {Space} from 'types/docs';

import PermissionsTab from './permissions_tab';
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

    // Renders a separator above the item, grouping the destructive action away
    // from the settings sections.
    separated?: boolean;

    destructive?: boolean;
};

const SpaceSettingsModal = ({space, onClose, initialTab = 'info'}: Props) => {
    const {formatMessage} = useIntl();
    const {paths: absolutePaths} = useDocsNavigation({absolute: true});
    const [activeTab, setActiveTab] = useState<SpaceSettingsTab>(initialTab);
    const canArchive = useCanDeleteSpace(space.id);
    const visibleActiveTab = activeTab === 'archive' && !canArchive ? 'info' : activeTab;

    const tabs: TabDef[] = [
        {id: 'info', label: formatMessage({id: 'docs.spaceSettings.tab.info', defaultMessage: 'Info'}), icon: InformationOutlineIcon},
        {id: 'permissions', label: formatMessage({id: 'docs.spaceSettings.tab.permissions', defaultMessage: 'Permissions'}), icon: ShieldOutlineIcon},
        {id: 'configuration', label: formatMessage({id: 'docs.spaceSettings.tab.configuration', defaultMessage: 'Configuration'}), icon: CogOutlineIcon},
    ];

    // Gated on the delete tier rather than on having reached this modal at all. Opening the modal
    // takes the manage tier, which is a different team permission — a team administrator can hold
    // one without the other, so reaching these settings says nothing about whether the archive
    // route would admit the caller.
    if (canArchive) {
        tabs.push({id: 'archive', label: formatMessage({id: 'docs.spaceSettings.tab.archive', defaultMessage: 'Archive space'}), icon: ArchiveOutlineIcon, separated: true, destructive: true});
    }

    const info = useInfoTab(space);
    const [discarding, setDiscarding] = useState(false);

    // Guard the close affordances (backdrop, Esc, ✕) when there are unsaved
    // edits, confirming a discard before actually closing.
    const handleClose = () => {
        if (info.dirty) {
            setDiscarding(true);
            return;
        }
        onClose();
    };

    const title = (
        <FormattedMessage
            id='docs.spaceSettings.title'
            defaultMessage='Space Settings'
        />
    );

    return (
        <GenericModal
            className={styles.modal}
            title={title}
            ariaLabel={formatMessage({id: 'docs.spaceSettings.title', defaultMessage: 'Space Settings'})}
            onClose={handleClose}
        >
            <Tabs
                className={styles.body}
                orientation='vertical'
                value={visibleActiveTab}
                onValueChange={(value) => setActiveTab(value as SpaceSettingsTab)}
            >
                <TabList
                    className={styles.nav}
                    aria-label={formatMessage({id: 'docs.spaceSettings.navLabel', defaultMessage: 'Space settings sections'})}
                >
                    {tabs.map((tab) => {
                        const Icon = tab.icon;
                        return (
                            <React.Fragment key={tab.id}>
                                {tab.separated && <TabsSeparator/>}
                                <Tab
                                    value={tab.id}
                                    leadingIcon={<Icon size={16}/>}
                                    destructive={tab.destructive}
                                >
                                    {tab.label}
                                </Tab>
                            </React.Fragment>
                        );
                    })}
                </TabList>

                <div className={styles.pane}>
                    <TabPanel
                        value='info'
                        className={styles.panelContent}
                    >
                        <InfoTab
                            space={space}
                            info={info}
                            url={absolutePaths.space(space.id)}
                        />
                    </TabPanel>
                    <TabPanel
                        value='permissions'
                        className={styles.panelContent}
                    >
                        <PermissionsTab
                            space={space}
                            onClose={onClose}
                        />
                    </TabPanel>
                    <TabPanel
                        value='configuration'
                        className={styles.panelContent}
                    >
                        <ConfigurationTab/>
                    </TabPanel>
                    {canArchive && (
                        <TabPanel
                            value='archive'
                            className={styles.panelContent}
                        >
                            <ArchiveTab
                                space={space}
                                onClose={onClose}
                            />
                        </TabPanel>
                    )}
                </div>
            </Tabs>
            {info.dirty && (
                <div className={styles.floatingFooter}>
                    <SaveChangesBar
                        state={info.error ? 'error' : 'editing'}
                        errorMessage={info.error}
                        saving={info.saving}
                        onSave={info.save}
                        onReset={info.reset}
                    />
                </div>
            )}

            {/* Nested inside this dialog's tree so Base UI stacks it as a child
                dialog rather than a sibling at the app root. */}
            {discarding && (
                <ConfirmModal
                    isConfirmDestructive={true}
                    title={formatMessage({id: 'docs.spaceSettings.discard.title', defaultMessage: 'Discard unsaved changes?'})}
                    confirmButtonText={formatMessage({id: 'docs.spaceSettings.discard.confirm', defaultMessage: 'Discard changes'})}
                    onConfirm={() => {
                        setDiscarding(false);
                        onClose();
                    }}
                    onCancel={() => setDiscarding(false)}
                >
                    <FormattedMessage
                        id='docs.spaceSettings.discard.body'
                        defaultMessage='Your unsaved changes to this space will be lost.'
                    />
                </ConfirmModal>
            )}
        </GenericModal>
    );
};

export const Section = ({title, children}: {title: React.ReactNode; children: React.ReactNode}) => (
    <section className={styles.section}>
        <h2 className={styles.sectionTitle}>{title}</h2>
        {children}
    </section>
);

type InfoTabState = {
    name: string;
    setName: (value: string) => void;
    description: string;
    setDescription: (value: string) => void;

    // '' = the space front door (hero) rather than a specific page.
    landingPageId: string;
    setLandingPageId: (value: string) => void;
    error?: string;
    dirty: boolean;
    canSave: boolean;
    saving: boolean;
    save: () => void;
    reset: () => void;
};

// Owns the editable Info-tab fields and the save flow. The baseline is read live
// from the store (via getSpace) so a successful save clears the dirty state and
// the floating save bar without closing the modal. Save no longer closes; the
// user dismisses the modal themselves (guarded when there are unsaved changes).
function useInfoTab(space: Space): InfoTabState {
    const dispatch = useAppDispatch();
    const liveSpace = useAppSelector((state) => getSpace(state, space.id)) ?? space;

    const savedLandingPageId = liveSpace.props?.[SPACE_PROP_DEFAULT_PAGE_ID] ?? '';

    const [name, setName] = useState(space.title);
    const [description, setDescription] = useState(space.description ?? '');
    const [landingPageId, setLandingPageId] = useState(savedLandingPageId);
    const [error, setError] = useState<string>();
    const [saving, setSaving] = useState(false);

    const dirty = name.trim() !== liveSpace.title ||
        description.trim() !== (liveSpace.description ?? '') ||
        landingPageId !== savedLandingPageId;
    const canSave = dirty && Boolean(name.trim()) && !saving;

    const save = async () => {
        if (!canSave) {
            return;
        }
        setSaving(true);
        setError(undefined);
        try {
            // Props replace wholesale server-side, so merge onto the live map to
            // avoid dropping keys this UI doesn't manage.
            const props = {...liveSpace.props, [SPACE_PROP_DEFAULT_PAGE_ID]: landingPageId};
            await dispatch(updateSpace(space.id, {title: name.trim(), description: description.trim(), props}));
        } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
        } finally {
            setSaving(false);
        }
    };

    const reset = () => {
        setName(liveSpace.title);
        setDescription(liveSpace.description ?? '');
        setLandingPageId(savedLandingPageId);
        setError(undefined);
    };

    return {name, setName, description, setDescription, landingPageId, setLandingPageId, error, dirty, canSave, saving, save, reset};
}

const InfoTab = ({space, info, url}: {space: Space; info: InfoTabState; url: string}) => {
    const {formatMessage} = useIntl();

    // The landing-page options need the space's pages, which aren't guaranteed
    // loaded when settings is opened from outside the space view.
    const dispatch = useAppDispatch();
    useEffect(() => {
        dispatch(fetchPages(space.id));
    }, [dispatch, space.id]);

    const pages = useAppSelector((state) => getPagesForSpace(state, space.id));

    const landingPageOptions: SelectOption[] = useMemo(() => [
        {
            value: '',
            label: formatMessage({id: 'docs.spaceSettings.info.landingSpaceHome', defaultMessage: 'Space home'}),
            leadingIcon: <HomeVariantOutlineIcon size={16}/>,
        },
        ...pages.map((page) => ({
            value: page.id,
            label: page.title,
            leadingIcon: <FileTextOutlineIcon size={16}/>,
        })),
    ], [pages, formatMessage]);

    return (
        <Section
            title={(
                <FormattedMessage
                    id='docs.spaceSettings.info.heading'
                    defaultMessage='Space info'
                />
            )}
        >
            <div className={styles.field}>
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
                <div className={styles.urlRow}>
                    <span className={styles.helper}>
                        <FormattedMessage
                            id='docs.spaceSettings.info.urlLabel'
                            defaultMessage='Space URL'
                        />
                    </span>
                    <span className={styles.urlText}>{url}</span>
                    <Button
                        emphasis='quaternary'
                        size='xs'
                        disabled={true}
                        className={styles.inlineButton}
                    >
                        <FormattedMessage
                            id='docs.spaceSettings.info.editUrl'
                            defaultMessage='Edit'
                        />
                    </Button>
                </div>
            </div>

            <div className={styles.field}>
                <span className={styles.fieldLabel}>
                    <FormattedMessage
                        id='docs.spaceSettings.info.descriptionLabel'
                        defaultMessage='Description'
                    />
                </span>
                <TextArea
                    id='docs-space-settings-description'
                    label={formatMessage({id: 'docs.spaceSettings.info.descriptionPlaceholder', defaultMessage: 'Enter a description for this space…'})}
                    value={info.description}
                    onChange={info.setDescription}
                    maxLength={1024}
                    rows={3}
                />
                <p className={styles.helper}>
                    <FormattedMessage
                        id='docs.spaceSettings.info.descriptionHelper'
                        defaultMessage='Describe how this space should be used. This will show when browsing spaces.'
                    />
                </p>
            </div>

            <Select
                id='docs-space-settings-landing-page'
                label={formatMessage({id: 'docs.spaceSettings.info.landingLabel', defaultMessage: 'Default landing page'})}
                value={info.landingPageId}
                options={landingPageOptions}
                onChange={info.setLandingPageId}
            />

        </Section>
    );
};

const ConfigurationTab = () => (
    <Section
        title={(
            <FormattedMessage
                id='docs.spaceSettings.configuration.heading'
                defaultMessage='Configuration'
            />
        )}
    >
        <p className={styles.copy}>
            <FormattedMessage
                id='docs.spaceSettings.configuration.comingSoon'
                defaultMessage='Space configuration options are coming soon.'
            />
        </p>
    </Section>
);

const ArchiveTab = ({space, onClose}: {space: Space; onClose: () => void}) => {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();
    const {goHome} = useDocsNavigation();
    const [error, setError] = useState<string>();
    const [confirming, setConfirming] = useState(false);

    const confirmArchive = async () => {
        setError(undefined);
        try {
            await dispatch(deleteSpace(space.id));
        } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
            setConfirming(false);
            return;
        }

        setConfirming(false);
        onClose();
        goHome();
    };

    return (
        <Section
            title={(
                <FormattedMessage
                    id='docs.spaceSettings.archive.heading'
                    defaultMessage='Archive space'
                />
            )}
        >
            <div className={styles.archiveCard}>
                <p className={styles.copy}>
                    <FormattedMessage
                        id='docs.spaceSettings.archive.copy'
                        defaultMessage='Archiving removes this space and its pages from the team. Members will lose access. You can ask an admin to restore it later.'
                    />
                </p>
                <DestructiveButton
                    leadingIcon={<ArchiveOutlineIcon size={16}/>}
                    onClick={() => setConfirming(true)}
                >
                    <FormattedMessage
                        id='docs.spaceSettings.archive.button'
                        defaultMessage='Archive space'
                    />
                </DestructiveButton>
                {error && <div className={styles.error}>{error}</div>}
            </div>

            {/* Rendered inside this modal's tree (not through the modal
                controller, which mounts at the app root) so Base UI sees it as a
                nested dialog and stacks it over the settings modal. */}
            {confirming && (
                <ConfirmModal
                    isConfirmDestructive={true}
                    title={formatMessage({id: 'docs.spaceSettings.archive.confirmTitle', defaultMessage: 'Archive this space?'})}
                    confirmButtonText={formatMessage({id: 'docs.spaceSettings.archive.confirmButton', defaultMessage: 'Archive'})}
                    onConfirm={confirmArchive}
                    onCancel={() => setConfirming(false)}
                >
                    <FormattedMessage
                        id='docs.spaceSettings.archive.confirmBody'
                        defaultMessage='“{title}” and its pages will be archived and members will lose access. This can be undone by an admin.'
                        values={{title: space.title}}
                    />
                </ConfirmModal>
            )}
        </Section>
    );
};

export default SpaceSettingsModal;

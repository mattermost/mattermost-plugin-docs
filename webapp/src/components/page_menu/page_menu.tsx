// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useIsFavorite, useToggleFavorite} from 'hooks/favorites';
import {useDocsNavigation} from 'hooks/navigation';
import {useAppDispatch} from 'hooks/redux';
import React, {useCallback} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {copyToClipboard} from 'utils/clipboard';

import AccountMultipleOutlineIcon from '@mattermost/compass-icons/components/account-multiple-outline';
import BellOutlineIcon from '@mattermost/compass-icons/components/bell-outline';
import ClockOutlineIcon from '@mattermost/compass-icons/components/clock-outline';
import ContentCopyIcon from '@mattermost/compass-icons/components/content-copy';
import DockWindowIcon from '@mattermost/compass-icons/components/dock-window';
import ExportVariantIcon from '@mattermost/compass-icons/components/export-variant';
import FolderMoveOutlineIcon from '@mattermost/compass-icons/components/folder-move-outline';
import LinkVariantIcon from '@mattermost/compass-icons/components/link-variant';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';
import StarIcon from '@mattermost/compass-icons/components/star';
import StarOutlineIcon from '@mattermost/compass-icons/components/star-outline';
import TrashCanOutlineIcon from '@mattermost/compass-icons/components/trash-can-outline';

import {updatePage} from 'store/actions';

import BasicInputModal from 'components/basic_input_modal/basic_input_modal';
import DeletePageModal from 'components/delete_page_modal/delete_page_modal';
import Menu from 'components/menu/menu';
import {openDocsModal} from 'components/modals';

// Mirrors the server's PageTitleMaxRunes (server/model/page.go).
const PAGE_TITLE_MAX_LENGTH = 255;

type Props = {
    spaceId: string;
    pageId: string;
    pageTitle: string;

    // Base UI merges its own open/aria/ref props onto the trigger element.
    trigger: React.ReactElement;
    align?: 'left' | 'right';
    tooltip?: string;

    // Controlled open state, for callers that open the menu from a keyboard
    // shortcut rather than the trigger (see the page tree's rows).
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
};

/**
 * The page actions menu, shared by the page tree rows and the page header.
 * Items without a handler are scaffolding for features that have no API yet.
 */
const PageMenu = ({spaceId, pageId, pageTitle, trigger, align = 'left', tooltip, open, onOpenChange}: Props) => {
    const {formatMessage} = useIntl();
    const {paths} = useDocsNavigation();
    const dispatch = useAppDispatch();
    const favorited = useIsFavorite('page', pageId);
    const toggleFavorite = useToggleFavorite();

    const pageUrl = `${window.location.origin}${paths.page(spaceId, pageId)}`;

    const copyLink = useCallback(() => copyToClipboard(pageUrl), [pageUrl]);
    const openInNewWindow = useCallback(() => window.open(pageUrl, '_blank', 'noopener,noreferrer'), [pageUrl]);

    const openRename = useCallback(() => {
        openDocsModal((modal) => (
            <BasicInputModal
                title={formatMessage({id: 'docs.renamePage.title', defaultMessage: 'Rename page'})}
                label={formatMessage({id: 'docs.renamePage.label', defaultMessage: 'Page name'})}
                initialValue={pageTitle}
                confirmButtonText={formatMessage({id: 'docs.renamePage.confirm', defaultMessage: 'Save'})}
                maxLength={PAGE_TITLE_MAX_LENGTH}
                onConfirm={async (title) => {
                    await dispatch(updatePage(spaceId, pageId, {title}));
                }}
                onClose={modal.close}
            />
        ));
    }, [dispatch, formatMessage, pageId, pageTitle, spaceId]);

    const openDeleteConfirm = useCallback(() => {
        openDocsModal((modal) => (
            <DeletePageModal
                spaceId={spaceId}
                pageId={pageId}
                pageTitle={pageTitle}
                onClose={modal.close}
            />
        ));
    }, [pageId, pageTitle, spaceId]);

    return (
        <Menu
            ariaLabel={formatMessage({id: 'docs.pageMenu.label', defaultMessage: 'Page options for {title}'}, {title: pageTitle})}
            align={align}
            tooltip={tooltip}
            trigger={trigger}
            open={open}
            onOpenChange={onOpenChange}
        >
            <Menu.Item
                leadingIcon={favorited ? <StarIcon size={18}/> : <StarOutlineIcon size={18}/>}
                onClick={() => toggleFavorite('page', pageId)}
            >
                {favorited ? (
                    <FormattedMessage
                        id='docs.pageMenu.unfavorite'
                        defaultMessage='Remove from favorites'
                    />
                ) : (
                    <FormattedMessage
                        id='docs.pageMenu.favorite'
                        defaultMessage='Favorite'
                    />
                )}
            </Menu.Item>
            <Menu.Item
                leadingIcon={<PencilOutlineIcon size={18}/>}
                onClick={openRename}
            >
                <FormattedMessage
                    id='docs.pageMenu.rename'
                    defaultMessage='Rename'
                />
            </Menu.Item>
            <Menu.Item
                leadingIcon={<LinkVariantIcon size={18}/>}
                onClick={copyLink}
            >
                <FormattedMessage
                    id='docs.pageMenu.copyLink'
                    defaultMessage='Copy link'
                />
            </Menu.Item>

            <Menu.Separator/>

            <Menu.Item leadingIcon={<AccountMultipleOutlineIcon size={18}/>}>
                <FormattedMessage
                    id='docs.pageMenu.sharing'
                    defaultMessage='Sharing and Permissions'
                />
            </Menu.Item>
            <Menu.Submenu
                leadingIcon={<BellOutlineIcon size={18}/>}
                label={formatMessage({id: 'docs.pageMenu.notifications', defaultMessage: 'Notifications'})}
            >
                <Menu.Item>
                    <FormattedMessage
                        id='docs.pageMenu.notifications.all'
                        defaultMessage='All activity'
                    />
                </Menu.Item>
                <Menu.Item>
                    <FormattedMessage
                        id='docs.pageMenu.notifications.mentions'
                        defaultMessage='Mentions'
                    />
                </Menu.Item>
                <Menu.Item>
                    <FormattedMessage
                        id='docs.pageMenu.notifications.none'
                        defaultMessage='Nothing'
                    />
                </Menu.Item>
            </Menu.Submenu>
            <Menu.Item leadingIcon={<ClockOutlineIcon size={18}/>}>
                <FormattedMessage
                    id='docs.pageMenu.versionHistory'
                    defaultMessage='Version history'
                />
            </Menu.Item>

            <Menu.Separator/>

            <Menu.Item leadingIcon={<ContentCopyIcon size={18}/>}>
                <FormattedMessage
                    id='docs.pageMenu.duplicate'
                    defaultMessage='Duplicate page'
                />
            </Menu.Item>
            <Menu.Submenu
                leadingIcon={<FolderMoveOutlineIcon size={18}/>}
                label={formatMessage({id: 'docs.pageMenu.moveTo', defaultMessage: 'Move to'})}
            >
                <Menu.Item disabled={true}>
                    <FormattedMessage
                        id='docs.pageMenu.comingSoon'
                        defaultMessage='Coming soon'
                    />
                </Menu.Item>
            </Menu.Submenu>
            <Menu.Submenu
                leadingIcon={<ExportVariantIcon size={18}/>}
                label={formatMessage({id: 'docs.pageMenu.export', defaultMessage: 'Export'})}
            >
                <Menu.Item>
                    <FormattedMessage
                        id='docs.pageMenu.export.pdf'
                        defaultMessage='PDF'
                    />
                </Menu.Item>
                <Menu.Item>
                    <FormattedMessage
                        id='docs.pageMenu.export.markdown'
                        defaultMessage='Markdown'
                    />
                </Menu.Item>
            </Menu.Submenu>

            <Menu.Item
                leadingIcon={<TrashCanOutlineIcon size={18}/>}
                destructive={true}
                onClick={openDeleteConfirm}
            >
                <FormattedMessage
                    id='docs.pageMenu.delete'
                    defaultMessage='Delete page'
                />
            </Menu.Item>

            <Menu.Separator/>

            <Menu.Item
                leadingIcon={<DockWindowIcon size={18}/>}
                onClick={openInNewWindow}
            >
                <FormattedMessage
                    id='docs.pageMenu.openInNewWindow'
                    defaultMessage='Open in new window'
                />
            </Menu.Item>
        </Menu>
    );
};

export default PageMenu;

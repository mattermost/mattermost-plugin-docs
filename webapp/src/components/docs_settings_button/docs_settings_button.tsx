// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useAppDispatch} from 'hooks/redux';
import React from 'react';
import {useIntl} from 'react-intl';

import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {openUserSettings, SETTINGS_BUTTON_ID} from 'actions/user_menu';

import styles from './docs_settings_button.module.scss';

const DocsSettingsButton = () => {
    const {formatMessage} = useIntl();
    const dispatch = useAppDispatch();

    const label = formatMessage({id: 'docs.header.settings', defaultMessage: 'Settings'});

    return (
        <WithTooltip title={label}>
            <button
                id={SETTINGS_BUTTON_ID}
                type='button'
                className={styles.button}
                aria-label={label}
                aria-haspopup='dialog'
                onClick={() => dispatch(openUserSettings())}
            >
                <SettingsOutlineIcon size={18}/>
            </button>
        </WithTooltip>
    );
};

export default DocsSettingsButton;

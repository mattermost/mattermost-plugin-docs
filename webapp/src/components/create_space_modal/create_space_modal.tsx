// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCreateSpace} from 'hooks/spaces';
import React from 'react';
import {useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';
import {SPACE_DESCRIPTION_MAX_LENGTH, SPACE_NAME_MAX_LENGTH} from 'validation/space_schema';

import GlobeIcon from '@mattermost/compass-icons/components/globe';
import LockOutlineIcon from '@mattermost/compass-icons/components/lock-outline';

import {PrimaryButton, TertiaryButton} from 'components/form-controls/button';
import type {SelectorOption} from 'components/form-controls/public_private_selector';
import PublicPrivateSelector from 'components/form-controls/public_private_selector';
import TextArea from 'components/form-controls/text_area';
import TextInput from 'components/form-controls/text_input';
import GenericModal from 'components/generic_modal/generic_modal';

import type {Space, SpaceVisibility} from 'types/docs';

import styles from './create_space_modal.module.scss';
import {firstSpaceValidationError} from './validation_messages';

type Props = {
    onClose: () => void;
    onCreated?: (space: Space) => void;
};

const CreateSpaceModal = ({onClose, onCreated}: Props) => {
    const {formatMessage} = useIntl();

    const {form, changeName, submit} = useCreateSpace({
        onCreated: (space) => {
            onCreated?.(space);
            onClose();
        },
    });

    const visibilityOptions: SelectorOption[] = [
        {
            value: 'public',
            icon: <GlobeIcon size={32}/>,
            title: formatMessage({id: 'docs.createSpace.public.title', defaultMessage: 'Public Space'}),
            description: formatMessage({id: 'docs.createSpace.public.description', defaultMessage: 'Any team member can view'}),
        },
        {
            value: 'private',
            icon: <LockOutlineIcon size={32}/>,
            title: formatMessage({id: 'docs.createSpace.private.title', defaultMessage: 'Private Space'}),
            description: formatMessage({id: 'docs.createSpace.private.description', defaultMessage: 'Only invited members'}),

            // Private spaces require space-level permissions, which are not built
            // yet — shown but disabled for the initial MVF.
            disabled: true,
            disabledReason: formatMessage({id: 'docs.createSpace.private.disabledReason', defaultMessage: 'Private spaces are coming soon'}),
        },
    ];

    const footer = (
        <>
            <TertiaryButton
                type='button'
                onClick={onClose}
            >
                {formatMessage({id: 'docs.createSpace.cancel', defaultMessage: 'Cancel'})}
            </TertiaryButton>
            <form.Subscribe selector={(state) => ({canSubmit: state.canSubmit, nameFilled: state.values.name.trim().length > 0})}>
                {({canSubmit, nameFilled}) => (
                    <PrimaryButton
                        type='button'
                        disabled={!canSubmit || !nameFilled}
                        onClick={submit}
                    >
                        {formatMessage({id: 'docs.createSpace.create', defaultMessage: 'Create'})}
                    </PrimaryButton>
                )}
            </form.Subscribe>
        </>
    );

    return (
        <GenericModal
            className={styles.modal}
            headerClassName={styles.header}
            title={formatMessage({id: 'docs.createSpace.title', defaultMessage: 'Create a new space'})}
            onClose={onClose}
            footer={footer}
        >
            <div className={styles.body}>
                <div className={styles.group}>
                    <form.Field name='name'>
                        {(field) => (
                            <TextInput
                                id='docs-create-space-name'
                                label={formatMessage({id: 'docs.createSpace.nameLabel', defaultMessage: 'Space name'})}
                                value={field.state.value}
                                onChange={changeName}
                                leading={<span aria-hidden='true'><SpaceIcon size={20}/></span>}
                                error={firstSpaceValidationError(field.state.meta.errors, formatMessage)}
                                maxLength={SPACE_NAME_MAX_LENGTH}
                                autoFocus={true}
                                onEnter={submit}
                            />
                        )}
                    </form.Field>
                </div>

                <div className={styles.group}>
                    <form.Field name='visibility'>
                        {(field) => (
                            <PublicPrivateSelector
                                ariaLabel={formatMessage({id: 'docs.createSpace.visibilityLabel', defaultMessage: 'Space visibility'})}
                                options={visibilityOptions}
                                value={field.state.value}
                                onChange={(value) => field.handleChange(value as SpaceVisibility)}
                            />
                        )}
                    </form.Field>
                    <p className={styles.note}>
                        {formatMessage({id: 'docs.createSpace.permissionsNote', defaultMessage: 'Specific edit and sharing permissions can be defined once the space is created.'})}
                    </p>
                </div>

                <div className={styles.group}>
                    <form.Field name='description'>
                        {(field) => (
                            <TextArea
                                id='docs-create-space-description'
                                label={formatMessage({id: 'docs.createSpace.descriptionPlaceholder', defaultMessage: 'Enter a description (optional)'})}
                                value={field.state.value}
                                onChange={field.handleChange}
                                error={firstSpaceValidationError(field.state.meta.errors, formatMessage)}
                                maxLength={SPACE_DESCRIPTION_MAX_LENGTH}
                            />
                        )}
                    </form.Field>
                    <p className={styles.note}>
                        {formatMessage({id: 'docs.createSpace.descriptionHelp', defaultMessage: 'This will be displayed when browsing for spaces'})}
                    </p>
                </div>
            </div>
        </GenericModal>
    );
};

export default CreateSpaceModal;

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';
import {useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';

import type {Space} from 'types/docs';

import MemberAvatars from './member_avatars';
import styles from './page_hero.module.scss';

type Stat = {
    key: string;
    value: number | undefined;
    label: string;
};

type Props = {
    space: Space;
    pageCount: number;
    memberCount: number;
};

// Space front-door banner: icon + title, description, and a stats row. Page and
// member counts are wired to the server; views has no source yet, so it renders
// an em dash.
const PageHero = ({space, pageCount, memberCount}: Props) => {
    const {formatMessage} = useIntl();

    const stats: Stat[] = [
        {key: 'pages', value: pageCount, label: formatMessage({id: 'docs.space.stat.pages', defaultMessage: 'Pages'})},
        {key: 'members', value: memberCount, label: formatMessage({id: 'docs.space.stat.members', defaultMessage: 'Members'})},
        {key: 'views', value: undefined, label: formatMessage({id: 'docs.space.stat.views', defaultMessage: 'Views'})},
    ];

    return (
        <section className={styles.hero}>
            <div className={styles.heading}>
                <span
                    className={styles.iconTile}
                    aria-hidden={true}
                >
                    <SpaceIcon
                        space={space}
                        size={28}
                    />
                </span>
                <h1 className={styles.title}>{space.title}</h1>
            </div>

            {space.description ? (
                <p className={styles.description}>{space.description}</p>
            ) : (
                <p className={classNames(styles.description, styles.descriptionMuted)}>
                    {formatMessage({id: 'docs.space.descriptionPlaceholder', defaultMessage: 'Add a space description here — just a brief summary of the purpose for this space.'})}
                </p>
            )}

            <div className={styles.stats}>
                {stats.map((stat) => (
                    <div
                        key={stat.key}
                        className={styles.stat}
                    >
                        {/* eslint-disable-next-line formatjs/no-literal-string-in-jsx -- em dash placeholder for the view count, which has no server source yet */}
                        <span className={styles.statValue}>{stat.value ?? '—'}</span>
                        <span className={styles.statLabel}>{stat.label}</span>
                    </div>
                ))}
                <div className={styles.avatars}>
                    <MemberAvatars spaceId={space.id}/>
                </div>
            </div>
        </section>
    );
};

export default PageHero;

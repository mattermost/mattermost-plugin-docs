// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useDocsNavigation} from 'hooks/navigation';
import {useAppSelector} from 'hooks/redux';
import {useRecentSpaceSummaries} from 'hooks/spaces';
import {useCurrentUser} from 'hooks/user';
import React from 'react';
import {FormattedMessage, defineMessages, useIntl} from 'react-intl';
import {SpaceIcon} from 'utils/space_icon';
import {Timestamp} from 'webapp_globals';
import type {TimestampUnit} from 'webapp_globals';

import CreationOutlineIcon from '@mattermost/compass-icons/components/creation-outline';
import NotebookOutlineIcon from '@mattermost/compass-icons/components/notebook-outline';
import PlusIcon from '@mattermost/compass-icons/components/plus';
import SearchListIcon from '@mattermost/compass-icons/components/search-list';

import {areSpacesLoadedForCurrentTeam} from 'store/selectors';

import {PrimaryButton, TertiaryButton} from 'components/form_controls/button';
import Header from 'components/header/header';

import type {SpaceSummary} from 'types/docs';

import styles from './docs_home.module.scss';

type Props = {
    onCreateSpace: () => void;

    // Browse is not built yet; optional so the CTA can be wired in later.
    onBrowseSpaces?: () => void;
};

// The "recently updated" pages table is a follow-up — it needs page
// author/status data that isn't modeled yet.
const DocsHome = ({onCreateSpace, onBrowseSpaces}: Props) => {
    const {formatMessage} = useIntl();
    const {name} = useCurrentUser();
    const {goToSpace} = useDocsNavigation();
    const summaries = useRecentSpaceSummaries();
    const spacesLoaded = useAppSelector(areSpacesLoadedForCurrentTeam);

    const header = (
        <Header
            left={
                <h1 className={styles.headerTitle}>
                    {formatMessage({id: 'docs.home.title', defaultMessage: 'Home'})}
                </h1>
            }
            right={
                <PrimaryButton
                    className={styles.cta}
                    onClick={onCreateSpace}
                >
                    <PlusIcon size={16}/>
                    {formatMessage({id: 'docs.home.newSpace', defaultMessage: 'New Space'})}
                </PrimaryButton>
            }
        />
    );

    // Only an empty list that's actually settled means "no spaces". Until the
    // team's spaces arrive the list is empty for a different reason, and showing
    // the welcome hero would flash it at every returning user.
    if (!spacesLoaded) {
        return (
            <div className={styles.root}>
                {header}
            </div>
        );
    }

    if (summaries.length === 0) {
        return (
            <div className={styles.root}>
                {header}
                <div className={styles.scroll}>
                    <EmptyHero
                        onCreateSpace={onCreateSpace}
                        onBrowseSpaces={onBrowseSpaces}
                    />
                    <InfoCards/>
                </div>
            </div>
        );
    }

    return (
        <div className={styles.root}>
            {header}
            <div className={styles.scroll}>
                <Greeting name={name}/>
                <section className={styles.section}>
                    <div className={styles.sectionHeader}>
                        <h2 className={styles.sectionTitle}>
                            {formatMessage({id: 'docs.home.recentSpaces.title', defaultMessage: 'Recently viewed spaces'})}
                        </h2>
                        <button
                            type='button'
                            className={styles.sectionAction}
                            onClick={onBrowseSpaces}
                        >
                            {formatMessage({id: 'docs.home.recentSpaces.browseMore', defaultMessage: 'Browse more'})}
                        </button>
                    </div>
                    <div className={styles.spaceGrid}>
                        {summaries.map((summary) => (
                            <SpaceCard
                                key={summary.space.id}
                                summary={summary}
                                onOpen={goToSpace}
                            />
                        ))}
                    </div>
                </section>
            </div>
        </div>
    );
};

const greetingMessages = defineMessages({
    morning: {id: 'docs.home.greeting.morning', defaultMessage: 'Good morning, {name}.'},
    afternoon: {id: 'docs.home.greeting.afternoon', defaultMessage: 'Good afternoon, {name}.'},
    evening: {id: 'docs.home.greeting.evening', defaultMessage: 'Good evening, {name}.'},
});

const Greeting = ({name}: {name: string}) => {
    const {formatMessage} = useIntl();

    const hour = new Date().getHours();
    let timeOfDay: keyof typeof greetingMessages = 'evening';
    if (hour < 12) {
        timeOfDay = 'morning';
    } else if (hour < 18) {
        timeOfDay = 'afternoon';
    }
    const greeting = formatMessage(greetingMessages[timeOfDay], {name});

    return (
        <section className={styles.hero}>
            <div className={styles.greetingText}>
                <h2 className={styles.greetingTitle}>{greeting}</h2>
                <p className={styles.greetingSubtitle}>
                    {formatMessage({id: 'docs.home.greeting.subtitle', defaultMessage: 'Pick up where you left off across your Spaces.'})}
                </p>
            </div>
            <div
                className={styles.heroArt}
                aria-hidden='true'
            >
                <NotebookOutlineIcon size={120}/>
            </div>
        </section>
    );
};

const justNow = (
    <FormattedMessage
        id='docs.home.space.justNow'
        defaultMessage='just now'
    />
);

// Relative-time buckets for the "Viewed …" label, handed to the host Timestamp.
// Negative bounds select past ranges; below 45s reads "just now".
const VIEWED_TIME_SPEC: TimestampUnit[] = [
    {within: ['second', -45], display: justNow},
    ['minute', -59],
    ['hour', -48],
    ['day', -30],
    ['month', -12],
    'year',
];

const SpaceCard = ({summary, onOpen}: {summary: SpaceSummary; onOpen: (id: string) => void}) => {
    const {formatMessage} = useIntl();
    const {space, pageCount, lastViewedAt} = summary;

    const pages = pageCount === undefined ? null : formatMessage(
        {id: 'docs.home.space.pageCount', defaultMessage: '{count, plural, one {# page} other {# pages}}'},
        {count: pageCount},
    );

    // Timestamp's `style` is a narrow/short/long format variant, not a DOM style object.
    /* eslint-disable react/style-prop-object */
    const relative = lastViewedAt === undefined ? null : (
        <Timestamp
            value={lastViewedAt}
            units={VIEWED_TIME_SPEC}
            useTime={false}
            style='narrow'
        />
    );
    /* eslint-enable react/style-prop-object */

    return (
        <button
            type='button'
            className={styles.spaceCard}
            onClick={() => onOpen(space.id)}
        >
            <span
                className={styles.spaceCardEmoji}
                aria-hidden='true'
            >
                <SpaceIcon
                    space={space}
                    size={20}
                />
            </span>
            <span className={styles.spaceCardText}>
                <span className={styles.spaceCardName}>{space.title}</span>
                <span className={styles.spaceCardMeta}>
                    {relative ? (
                        <>
                            {pages}

                            {pages ? (
                                // eslint-disable-next-line formatjs/no-literal-string-in-jsx -- decorative separator between metadata segments
                                <>{' · '}</>
                            ) : null}
                            <FormattedMessage
                                id='docs.home.space.viewed'
                                defaultMessage='Viewed {relative}'
                                values={{relative}}
                            />
                        </>
                    ) : pages}
                </span>
            </span>
        </button>
    );
};

const EmptyHero = ({onCreateSpace, onBrowseSpaces}: Props) => {
    const {formatMessage} = useIntl();

    return (
        <section className={styles.hero}>
            <div className={styles.heroText}>
                <div className={styles.heroCopy}>
                    <h2 className={styles.heroTitle}>
                        {formatMessage({id: 'docs.home.welcome.title', defaultMessage: 'Welcome to Docs.'})}
                    </h2>
                    <p className={styles.heroBody}>
                        {formatMessage({id: 'docs.home.welcome.body', defaultMessage: 'Long-form knowledge for your team — documentation, specs, handbooks, decisions, postmortems. Every page lives in a Space which can be linked with Channels for convenience.'})}
                    </p>
                </div>
                <div className={styles.heroActions}>
                    <PrimaryButton
                        size='lg'
                        className={styles.cta}
                        onClick={onCreateSpace}
                    >
                        <NotebookOutlineIcon size={18}/>
                        {formatMessage({id: 'docs.home.createSpace', defaultMessage: 'Create a space'})}
                    </PrimaryButton>
                    <TertiaryButton
                        size='lg'
                        onClick={onBrowseSpaces}
                    >
                        {formatMessage({id: 'docs.home.browseSpaces', defaultMessage: 'Browse spaces'})}
                    </TertiaryButton>
                </div>
            </div>

            {/* Stand-in for the Figma "documents" illustration; replace with the
                exported asset when available. Decorative. */}
            <div
                className={styles.heroArt}
                aria-hidden='true'
            >
                <NotebookOutlineIcon size={120}/>
            </div>
        </section>
    );
};

const InfoCards = () => {
    const {formatMessage} = useIntl();

    const cards = [
        {
            key: 'whatIsSpace',
            icon: <NotebookOutlineIcon size={24}/>,
            title: formatMessage({id: 'docs.home.card.whatIsSpace.title', defaultMessage: 'What is a Space?'}),
            body: formatMessage({id: 'docs.home.card.whatIsSpace.body', defaultMessage: 'A Space is a set of pages with its own permissions and structure.'}),
            link: formatMessage({id: 'docs.home.card.whatIsSpace.link', defaultMessage: 'Learn more'}),
        },
        {
            key: 'draft',
            icon: <CreationOutlineIcon size={24}/>,
            title: formatMessage({id: 'docs.home.card.draft.title', defaultMessage: 'Draft with Agents'}),
            body: formatMessage({id: 'docs.home.card.draft.body', defaultMessage: 'Ask Agents to generate a page from any context like a thread or channel.'}),
            link: formatMessage({id: 'docs.home.card.draft.link', defaultMessage: 'Try it now'}),
        },
        {
            key: 'browse',
            icon: <SearchListIcon size={24}/>,
            title: formatMessage({id: 'docs.home.card.browse.title', defaultMessage: 'Browse Spaces'}),
            body: formatMessage({id: 'docs.home.card.browse.body', defaultMessage: 'See what public spaces your team is already working on in your workspace.'}),
            link: formatMessage({id: 'docs.home.card.browse.link', defaultMessage: 'Browse'}),
        },
    ];

    return (
        <div className={styles.cards}>
            {cards.map((card) => (
                <div
                    key={card.key}
                    className={styles.card}
                >
                    <div className={styles.cardIcon}>{card.icon}</div>
                    <div className={styles.cardText}>
                        <div className={styles.cardHeading}>
                            <h3 className={styles.cardTitle}>{card.title}</h3>
                            <p className={styles.cardBody}>{card.body}</p>
                        </div>
                        <button
                            type='button'
                            className={styles.cardLink}
                        >
                            {card.link}
                        </button>
                    </div>
                </div>
            ))}
        </div>
    );
};

export default DocsHome;

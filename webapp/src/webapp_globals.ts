// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {History} from 'history';
import React from 'react';
import type {ComponentType, ElementType, ForwardRefExoticComponent, KeyboardEvent, KeyboardEventHandler, ReactElement, ReactNode, ReactNodeArray, RefAttributes, RefObject} from 'react';
import type {MessageDescriptor} from 'react-intl';
import type {Action} from 'redux';

import type {Agent} from '@mattermost/types/agents';
import type {Channel} from '@mattermost/types/channels';
import type {Group} from '@mattermost/types/groups';
import type {UserProfile} from '@mattermost/types/users';

// Hand-typed view of the API the host web app attaches to `window` for plugins
// (core's plugins/export.ts). The host guarantees these at runtime; anything
// missing degrades to a no-op.
//
// TRANSITION-MIGRATION: the modal and editor contracts below now live in core
// as @mattermost/shared/types/global (WindowShared, PublishedModalUtils,
// PublishedEditorUtils, PublishedSuggestionProviderConstructors, etc.). Our
// pinned @mattermost/shared release doesn't export them yet, so they're
// mirrored here. Replace this slice with those imports once the dependency is
// bumped to a version that ships types/global — which also lets openModalById
// be typed per-modal, and the editor components import from their canonical
// source instead of this local mirror. browserHistory has not migrated into
// WindowShared yet, so it stays here too.

type PublishedModalId = 'user_settings' | 'invitation' | 'team_settings' | 'team_members' | 'leave_team';

type PublishedModalUtils = {

    // The published shared contract types this `void`, but the runtime returns a
    // MODAL_OPEN Redux action the caller dispatches on the (shared) core store.
    openModalById: (modalId: PublishedModalId, dialogProps?: Record<string, unknown>) => Action | undefined;

    // Reports whether the running web app actually publishes this modal id. Takes
    // any string so a newer plugin can probe an id an older host doesn't publish.
    canOpenModalId: (modalId: string) => boolean;
};

export type ActionResult<Data = unknown, Error = unknown> = {
    data?: Data;
    error?: Error;
};

type Loading = {loading: boolean};

type ComponentOrComponents = {
    component: ElementType;
} | {
    components: ElementType[];
};

export type ProviderResultsGroup<Item = unknown> = {
    key: string;
    label?: MessageDescriptor;
    terms: string[];
    items: Array<Item | Loading>;
} & ComponentOrComponents;

export type ProviderResults<Item = unknown> =
    | {matchedPretext: string; groups: Array<ProviderResultsGroup<Item>>}
    | ({matchedPretext: string; terms: string[]; items: Array<Item | Loading>} & ComponentOrComponents);

type SuggestionResultsGroup<Item = unknown> = {
    key: string;
    label?: MessageDescriptor;
    terms: string[];
    items: Array<Item | Loading>;
    components: ElementType[];
};

export type SuggestionResults<Item = unknown> =
    | {matchedPretext: string; groups: Array<SuggestionResultsGroup<Item>>}
    | {matchedPretext: string; terms: string[]; items: Array<Item | Loading>; components: ElementType[]};

export type WysiwygEditorProps = {
    value: string;
    onChange: (markdown: string) => void;
    onSubmit: () => void;
    onFocus?: () => void;
    onBlur?: () => void;
    placeholder?: string;
    channelId: string;
    rootId?: string;
    disabled?: boolean;
    id?: string;
    useCtrlSend?: boolean;
    sendCodeBlockOnCtrlEnter?: boolean;
    onKeyDown?: (e: KeyboardEvent<HTMLDivElement>) => void;
};

export type SuggestionListProps = {
    inputRef?: RefObject<HTMLDivElement>;
    open: boolean;
    position?: 'top' | 'bottom';
    renderNoResults?: boolean;
    onCompleteWord: (term: string, matchedPretext: string, e?: KeyboardEventHandler<HTMLDivElement>) => boolean;
    preventClose?: () => void;
    onItemHover: (term: string) => void;
    pretext: string;
    cleared: boolean;
    results: SuggestionResults;
    selection: string;
    suggestionBoxAlgn?: {
        lineHeight?: number;
        pixelsToMoveX?: number;
        pixelsToMoveY?: number;
    };
};

export type PublishedMarkdownMode = 'bold' | 'italic' | 'link' | 'strike' | 'code' | 'heading' | 'quote' | 'ul' | 'ol';

export type FormattingBarProps = {
    applyFormatting: (mode: PublishedMarkdownMode) => void;
    disableControls: boolean;
    location: string;
    additionalControls?: ReactNodeArray;
    aiActionsMenu?: ReactNode;

    // Returns a Tiptap Editor. Left as `unknown` so consumers don't have to
    // depend on `@tiptap/react` transitively; cast at the call site.
    getEditor?: () => unknown;
};

export type PublishedWysiwygEditorHandle = {
    insertText: (text: string) => void;
    focus: () => void;
    blur: () => void;
    getInputBox: () => HTMLElement | null;
};

export type PublishedFormattingBarHandle = {
    openLinkPopover: () => void;
};

export type SuggestionProviderInstance = {
    triggerCharacter?: string;
    handlePretextChanged: (pretext: string, resultsCallback: (results: ProviderResults) => void) => boolean | void;
};

export type AtMentionProviderOptions = {
    currentUserId: string;
    channelId: string;
    autocompleteUsersInChannel: (prefix: string) => Promise<ActionResult>;
    useChannelMentions: boolean;
    autocompleteGroups: Group[] | null;
    searchAssociatedGroupsForReference: (prefix: string) => Promise<ActionResult<Group[]>>;
    priorityProfiles: UserProfile[] | undefined;
    defaultAgent?: Agent;
};

export type CommandProviderOptions = {
    teamId: string;
    channelId: string;
    rootId?: string;
};

export type ChannelMentionProviderArgs = [
    channelSearchFunc: (
        term: string,
        success: (channels: Channel[]) => void,
        error: () => void,
    ) => Promise<ActionResult>,
    delayChannelAutocomplete: boolean,
];

export type PublishedSuggestionProviderConstructors = {
    AtMention: new (options: AtMentionProviderOptions) => SuggestionProviderInstance;
    ChannelMention: new (...args: ChannelMentionProviderArgs) => SuggestionProviderInstance;
    Command: new (options: CommandProviderOptions) => SuggestionProviderInstance;
    Emoticon: new () => SuggestionProviderInstance;
};

export type PublishedSuggestionProviderId = keyof PublishedSuggestionProviderConstructors;

export type PublishedEditorUtils = {
    WysiwygEditor: ForwardRefExoticComponent<WysiwygEditorProps & RefAttributes<PublishedWysiwygEditorHandle>>;
    SuggestionList: ComponentType<SuggestionListProps>;
    FormattingBar: ForwardRefExoticComponent<FormattingBarProps & RefAttributes<PublishedFormattingBarHandle>>;
    providers: PublishedSuggestionProviderConstructors;
};

type WebappUtils = {
    browserHistory?: History;
    modals?: Partial<PublishedModalUtils>;
    editor?: Partial<PublishedEditorUtils>;
};

const webappUtils = (): WebappUtils => (window as unknown as {WebappUtils?: WebappUtils}).WebappUtils ?? {};

// Relative-range spec entries accepted by the host Timestamp `units` prop: a
// unit name, a [unit, value] tuple, or a range descriptor object. Mirrors
// core's RangeDescriptor without pulling in its private types.
export type TimestampUnit = string | [string, number] | {within?: [string, number]; equals?: [string, number]; display: ReactNode | [string] | [string, number]};

type TimestampProps = {
    value?: number | Date;
    units?: TimestampUnit[];
    useTime?: boolean | object;
    useDate?: boolean | object;
    useRelative?: boolean;
    style?: 'narrow' | 'short' | 'long';
    children?: ReactNode;
};

export type AvatarSize = 'xxs' | 'xs' | 'sm' | 'md' | 'lg' | 'xl' | 'xxl';

// Mirrors core's Avatar props (widgets/users/avatar). `name` is set to '' when a
// visible label already accompanies the avatar, so screen readers don't repeat it.
type AvatarProps = {
    url?: string;
    username?: string;
    size?: AvatarSize;
    name?: string;
};

// Core exposes shared React components to plugins on `window.Components`
// (core's plugins/export.ts). Timestamp renders localized times; Avatar renders
// a user's profile picture with the host's sizing/fallback.
type HostComponents = {
    Timestamp?: ComponentType<TimestampProps>;
    Avatar?: ComponentType<AvatarProps>;
};

const hostComponents = (): HostComponents => (window as unknown as {Components?: HostComponents}).Components ?? {};

// Renders the host Timestamp, or nothing on a host that doesn't publish it. The
// fallback lives here so callers just render <Timestamp/> without a null check.
export const Timestamp = (props: TimestampProps): ReactElement | null => {
    const HostTimestamp = hostComponents().Timestamp;
    return HostTimestamp ? React.createElement(HostTimestamp, props) : null;
};

// Renders the host Avatar, or nothing on a host that doesn't publish it. The
// fallback lives here so callers just render <Avatar/> without a null check.
export const Avatar = (props: AvatarProps): ReactElement | null => {
    const HostAvatar = hostComponents().Avatar;
    return HostAvatar ? React.createElement(HostAvatar, props) : null;
};

export function getBrowserHistory(): History | undefined {
    return webappUtils().browserHistory;
}

// Whether the running host publishes this modal id (false on hosts predating the
// opener), so callers can gate before opening.
export function hostCanOpenModal(modalId: string): boolean {
    return webappUtils().modals?.canOpenModalId?.(modalId) ?? false;
}

// The Redux action that opens a published core modal, or undefined when the host
// build predates the opener (callers no-op instead of crashing).
export function hostOpenModalAction(modalId: PublishedModalId, dialogProps?: Record<string, unknown>): Action | undefined {
    return webappUtils().modals?.openModalById?.(modalId, dialogProps);
}

// Whether the running host publishes the WYSIWYG editor surface. Older hosts
// (predating MM-69774) don't attach `editor` — callers should fall back to a
// read-only render or an "update your server" empty state.
export function hostCanUseEditor(): boolean {
    return Boolean(webappUtils().editor?.WysiwygEditor);
}

// The published editor components + suggestion provider constructors, or
// undefined when the host doesn't expose them. Fields are individually optional
// so a newer host can add pieces without breaking older plugin bundles.
export function hostGetEditor(): Partial<PublishedEditorUtils> | undefined {
    return webappUtils().editor;
}

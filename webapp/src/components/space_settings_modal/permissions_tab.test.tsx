// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, cleanup, fireEvent, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {makeSpace} from 'store/test_fixtures';

import {DocsModalController, closeAllDocsModals} from 'components/modals';

import PermissionsTab from './permissions_tab';

import {renderWithContext, type TestStateOptions} from '../../../tests/react_testing_utils';

const mockAddMembers = jest.fn();
const mockRemoveMember = jest.fn();
const mockLeave = jest.fn();
let mockManageMembersBusy = false;

jest.mock('hooks/space_members', () => ({
    useManageSpaceMembers: () => ({
        addMembers: mockAddMembers,
        removeMember: mockRemoveMember,
        leave: mockLeave,
        busy: mockManageMembersBusy,
    }),
}));

jest.mock('hooks/members', () => ({
    useSpaceMemberProfiles: () => [
        {id: 'me', displayName: 'Caleb', username: 'caleb', avatarUrl: ''},
        {id: 'u2', displayName: 'Ada', username: 'ada', avatarUrl: ''},
    ],
}));

// AddMembersField renders the real people picker, which pulls in mattermost-redux's
// user search actions (published ESM that jest doesn't transform). Stub at the hook
// boundary, as share_space_modal.test.tsx does.
jest.mock('hooks/user_search', () => ({
    useUserSearch: () => ({results: [], loading: false}),
}));

const mockSetDefaults = jest.fn();
const mockSetViewAccess = jest.fn();
const mockSetMemberGrants = jest.fn();

// Stubbed at the hook boundary like the others: the real hook loads over the network on
// mount, which a component test has no server for. Its own behaviour is covered by
// hooks/space_permissions.test.tsx.
let mockPermissionsState: Record<string, unknown>;

jest.mock('hooks/space_permissions', () => ({
    useSpacePermissions: () => mockPermissionsState,
}));

const space = makeSpace('space-1', 'Engineering');

// Removing and leaving confirm first, and the confirmation is rendered by the
// imperative modal layer rather than inline, so it needs the controller mounted.
const renderTab = (onClose = jest.fn(), state: TestStateOptions = {}) => renderWithContext(
    <>
        <PermissionsTab
            space={space}
            onClose={onClose}
        />
        <DocsModalController/>
    </>,
    {
        state: {
            license: {CustomPermissionsSchemes: 'true', GuestAccountsPermissions: 'true'},
            ...state,
        },
    },
);

const confirm = async (name: RegExp) => {
    fireEvent.click(await screen.findByRole('button', {name}));
};

// The matrix checkboxes are addressed by id rather than by label: the same permission vocabulary is
// rendered once for the space default and once per member, so a label lookup is ambiguous. The
// assertion here reports a checkbox that failed to render as such, instead of letting the cast
// carry a null into a property read further down the test.
const matrixCheckbox = (id: string): HTMLInputElement => {
    const element = document.getElementById(id);
    expect(element).toBeInstanceOf(HTMLInputElement);
    return element as HTMLInputElement;
};

describe('PermissionsTab', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        mockManageMembersBusy = false;
        mockLeave.mockResolvedValue(true);
        mockSetDefaults.mockResolvedValue(undefined);
        mockSetViewAccess.mockResolvedValue(undefined);
        mockSetMemberGrants.mockResolvedValue(undefined);
        mockPermissionsState = {
            defaults: ['create_page', 'edit_page'],
            viewAccess: 'open',
            members: new Map(),
            canAdminister: true,
            canManageMembers: true,
            loading: false,
            loadFailed: false,
            busy: false,
            setDefaults: mockSetDefaults,
            setMemberGrants: mockSetMemberGrants,
            setViewAccess: mockSetViewAccess,
        };
    });

    afterEach(() => act(() => {
        closeAllDocsModals();
    }));

    // The tab used to fake this with an aria-disabled div; it is a real control now.
    it('offers a working add control', () => {
        renderTab();

        expect(screen.getByRole('button', {name: 'Add'})).toBeInTheDocument();
    });

    // A roster mutation started from this surface (or the sibling Share modal, via the same
    // hook) must lock Add/Remove/Leave here too, or a second one dispatched from this tab could
    // race the first.
    it('disables Add/Remove/Leave while a roster mutation is in flight', () => {
        mockManageMembersBusy = true;
        renderTab();

        expect(screen.getByRole('button', {name: 'Add'})).toBeDisabled();

        fireEvent.click(screen.getByRole('button', {name: /Ada/}));
        expect(screen.getByRole('menuitem', {name: 'Remove from space'})).toHaveAttribute('aria-disabled', 'true');
    });

    it('shows the space\'s current access and permission set', () => {
        renderTab();

        expect(screen.getByRole('radio', {name: /Public/})).toHaveAttribute('aria-checked', 'true');
        expect(screen.getByRole('radio', {name: /Private/})).toHaveAttribute('aria-checked', 'false');

        // Reflects `defaults`, not every option: an unchecked box is a permission the
        // space does not grant.
        expect(screen.getByRole('checkbox', {name: 'Create pages'})).toBeChecked();
        expect(screen.getByRole('checkbox', {name: 'Edit pages'})).toBeChecked();
        expect(screen.getByRole('checkbox', {name: 'Comment on pages'})).not.toBeChecked();
        expect(screen.getByRole('checkbox', {name: 'Delete any page'})).not.toBeChecked();
    });

    it('changes view access through the hook, and follows the value that comes back', () => {
        const {rerender} = renderTab();

        fireEvent.click(screen.getByRole('radio', {name: /Private/}));
        expect(mockSetViewAccess).toHaveBeenCalledWith('private');

        // The control is driven by the resolved record, not by the click: until the server's
        // answer lands it still reads Public, and it is that answer the selection follows.
        expect(screen.getByRole('radio', {name: /Private/})).toHaveAttribute('aria-checked', 'false');

        mockPermissionsState = {...mockPermissionsState, viewAccess: 'private'};
        rerender(
            <>
                <PermissionsTab
                    space={space}
                    onClose={jest.fn()}
                />
                <DocsModalController/>
            </>,
        );

        expect(screen.getByRole('radio', {name: /Private/})).toHaveAttribute('aria-checked', 'true');
        expect(screen.getByRole('radio', {name: /Public/})).toHaveAttribute('aria-checked', 'false');
    });

    it('sends the whole permission set when one is toggled on', () => {
        renderTab();

        fireEvent.click(screen.getByRole('checkbox', {name: 'Comment on pages'}));

        // The set is replaced, not patched, and stays in the vocabulary's own order.
        expect(mockSetDefaults).toHaveBeenCalledWith(['create_page', 'comment_page', 'edit_page']);
    });

    it('sends the remaining set when one is toggled off', () => {
        renderTab();

        fireEvent.click(screen.getByRole('checkbox', {name: 'Create pages'}));

        expect(mockSetDefaults).toHaveBeenCalledWith(['edit_page']);
    });

    describe('without the custom permission schemes entitlement', () => {
        beforeEach(() => {
            mockPermissionsState = {
                ...mockPermissionsState,
                defaults: ['create_page', 'comment_page', 'edit_page', 'delete_own_page'],
            };
        });

        it('offers the three named tiers instead of arbitrary combinations', () => {
            renderTab(jest.fn(), {license: {}});

            expect(screen.queryByRole('checkbox', {name: 'Create pages'})).not.toBeInTheDocument();
            expect(screen.getByRole('radio', {name: 'Can edit'})).toBeChecked();
            expect(screen.getByRole('radio', {name: 'Can comment'})).not.toBeChecked();
            expect(screen.getByRole('radio', {name: 'Can view'})).not.toBeChecked();
            expect(screen.getByText('Custom permission combinations require a Professional or Enterprise license that includes guest account permissions.')).toBeInTheDocument();
        });

        it('deduplicates server defaults when selecting the matching tier', () => {
            mockPermissionsState = {...mockPermissionsState, defaults: ['comment_page', 'comment_page']};
            renderTab(jest.fn(), {license: {}});

            expect(screen.getByRole('radio', {name: 'Can comment'})).toBeChecked();
            expect(screen.queryByText(/This space currently uses a custom permission combination/)).not.toBeInTheDocument();
        });

        it('writes a tier atomically without passing through an unsupported combination', () => {
            renderTab(jest.fn(), {license: {}});

            fireEvent.click(screen.getByRole('radio', {name: 'Can comment'}));

            expect(mockSetDefaults).toHaveBeenCalledWith(['comment_page']);
        });

        it('allows a space left on a custom combination to return to a tier', () => {
            mockPermissionsState = {...mockPermissionsState, defaults: ['create_page', 'edit_page']};
            renderTab(jest.fn(), {license: {}});

            expect(screen.getAllByRole('radio', {name: /^Can (view|comment|edit)$/})).toHaveLength(3);
            expect(screen.getByText(/This space currently uses a custom permission combination/)).toBeInTheDocument();

            fireEvent.click(screen.getByRole('radio', {name: 'Can view'}));
            expect(mockSetDefaults).toHaveBeenCalledWith([]);
        });

        it('keeps granular controls beneath the tiers for the Professional SKU fallback', () => {
            renderTab(jest.fn(), {
                license: {
                    CustomPermissionsSchemes: 'false',
                    SkuShortName: 'professional',
                    GuestAccountsPermissions: 'true',
                },
            });

            expect(screen.getByRole('checkbox', {name: 'Create pages'})).toBeInTheDocument();
            expect(screen.getByRole('radio', {name: 'Can edit'})).toBeChecked();
        });

        // Every custom scheme also defines a guest role, which the server refuses to create without
        // the guest-permissions entitlement — so the combination controls are withheld with it.
        it('withholds the granular controls when the license lacks guest account permissions', () => {
            renderTab(jest.fn(), {license: {CustomPermissionsSchemes: 'true'}});

            expect(screen.queryByRole('checkbox', {name: 'Create pages'})).not.toBeInTheDocument();
            expect(screen.getByRole('radio', {name: 'Can edit'})).toBeChecked();
        });
    });

    // The tiers lead on a licensed install too; the checkboxes beneath them are the refinement,
    // and a set that matches no tier leaves every tier unchecked rather than mislabelling it.
    it('marks the matching tier above the granular defaults, or none for a custom set', () => {
        renderTab();

        expect(screen.getByRole('checkbox', {name: 'Create pages'})).toBeChecked();
        expect(screen.getByRole('radio', {name: 'Can edit'})).not.toBeChecked();
        expect(screen.getByRole('radio', {name: 'Can view'})).not.toBeChecked();

        fireEvent.click(screen.getByRole('radio', {name: 'Can edit'}));
        expect(mockSetDefaults).toHaveBeenCalledWith(['create_page', 'comment_page', 'edit_page', 'delete_own_page']);
    });

    // The tier is derived from the set, never stored alongside it, so dropping one permission out
    // of a tier's set leaves the tier behind: the space is on a combination no preset names, and
    // the radio that named it a moment ago cannot keep claiming to.
    it('clears the chosen tier when a permission is unticked out of its set', () => {
        mockPermissionsState = {
            ...mockPermissionsState,
            defaults: ['create_page', 'comment_page', 'edit_page', 'delete_own_page'],
        };
        renderTab();

        expect(screen.getByRole('radio', {name: 'Can edit'})).toBeChecked();

        fireEvent.click(matrixCheckbox('space-default-edit_page'));

        // The write drops only that permission, keeping the rest of the set and its order.
        expect(mockSetDefaults).toHaveBeenCalledWith(['create_page', 'comment_page', 'delete_own_page']);

        // Re-render on the set the server would return: no tier matches it any more.
        mockPermissionsState = {
            ...mockPermissionsState,
            defaults: ['create_page', 'comment_page', 'delete_own_page'],
        };
        cleanup();
        renderTab();

        expect(screen.getByRole('radio', {name: 'Can edit'})).not.toBeChecked();
        expect(screen.getByRole('radio', {name: 'Can comment'})).not.toBeChecked();
        expect(screen.getByRole('radio', {name: 'Can view'})).not.toBeChecked();
    });

    // The mirror of the test above: unticking the last permission out of a custom set lands on
    // the empty set, which *is* a tier, so the radio that named nothing a moment ago names it now.
    it('re-checks a tier when unticking lands the set back on one', () => {
        mockPermissionsState = {...mockPermissionsState, defaults: ['create_page']};
        const {rerender} = renderTab();

        expect(screen.getByRole('radio', {name: 'Can view'})).not.toBeChecked();

        fireEvent.click(matrixCheckbox('space-default-create_page'));
        expect(mockSetDefaults).toHaveBeenCalledWith([]);

        mockPermissionsState = {...mockPermissionsState, defaults: []};
        rerender(
            <>
                <PermissionsTab
                    space={space}
                    onClose={jest.fn()}
                />
                <DocsModalController/>
            </>,
        );

        expect(screen.getByRole('radio', {name: 'Can view'})).toBeChecked();
    });

    // Promotion runs through the same grant control as any other permission, so the whole path is
    // observable here: the write carries admin_space, and the standing the server returns is what
    // renames the row — the click does not rename it on its own.
    it('promotes a member to space administrator through the grant control', () => {
        mockPermissionsState = {
            ...mockPermissionsState,
            members: new Map([['u2', {user_id: 'u2', permissions: ['read_page'], granted_permissions: [], is_admin: false, is_guest: false, is_auto_joined: false}]]),
        };
        const {rerender} = renderTab();

        expect(screen.getByRole('button', {name: 'Member — more actions for Ada'})).toBeInTheDocument();
        expect(matrixCheckbox('member-u2-admin_space').checked).toBe(false);

        fireEvent.click(matrixCheckbox('member-u2-admin_space'));
        expect(mockSetMemberGrants).toHaveBeenCalledWith('u2', ['admin_space']);

        // Still a Member until the answer lands: the row follows the record, not the click.
        expect(screen.getByRole('button', {name: 'Member — more actions for Ada'})).toBeInTheDocument();

        mockPermissionsState = {
            ...mockPermissionsState,
            members: new Map([['u2', {
                user_id: 'u2',
                permissions: ['read_page', 'create_page', 'comment_page', 'edit_page', 'delete_own_page', 'delete_page', 'admin_space'],
                granted_permissions: ['admin_space'],
                is_admin: true,
                is_guest: false,
                is_auto_joined: false,
            }]]),
        };
        rerender(
            <>
                <PermissionsTab
                    space={space}
                    onClose={jest.fn()}
                />
                <DocsModalController/>
            </>,
        );

        expect(screen.getByRole('button', {name: 'Admin — more actions for Ada'})).toBeInTheDocument();
        expect(matrixCheckbox('member-u2-admin_space').checked).toBe(true);

        // The admin role carries the page permissions through the scheme rather than through a
        // grant, so the box stays unticked — but the row says why, or it would read as an
        // administrator who cannot delete a page.
        expect(matrixCheckbox('member-u2-delete_page').checked).toBe(false);
        expect(matrixCheckbox('member-u2-delete_page').closest('div')).toHaveTextContent('Also from their administrator role');
    });

    // A member can be demoted to guest server-wide while this panel is open. The server refuses
    // every grant to a guest, so the controls have to go with the demotion — a row left holding
    // checkboxes would offer writes that can only fail.
    it('withdraws a member\'s grant controls when they become a guest mid-session', () => {
        mockPermissionsState = {
            ...mockPermissionsState,
            members: new Map([['u2', {user_id: 'u2', permissions: ['read_page', 'create_page'], granted_permissions: ['create_page'], is_admin: false, is_guest: false, is_auto_joined: false}]]),
        };
        const {rerender} = renderTab();

        // Live to begin with: revoking the grant reaches the hook as a whole-set replacement.
        expect(matrixCheckbox('member-u2-create_page').checked).toBe(true);
        fireEvent.click(matrixCheckbox('member-u2-create_page'));
        expect(mockSetMemberGrants).toHaveBeenCalledWith('u2', []);

        mockPermissionsState = {
            ...mockPermissionsState,
            members: new Map([['u2', {user_id: 'u2', permissions: ['read_page'], granted_permissions: [], is_admin: false, is_guest: true, is_auto_joined: false}]]),
        };
        rerender(
            <>
                <PermissionsTab
                    space={space}
                    onClose={jest.fn()}
                />
                <DocsModalController/>
            </>,
        );

        expect(document.getElementById('member-u2-create_page')).toBeNull();
        expect(document.getElementById('member-u2-read_page-inherited')).toHaveTextContent('View pages');
        expect(screen.getByRole('button', {name: 'Guest — more actions for Ada'})).toBeInTheDocument();
    });

    // Authority can be revoked while the tab is open — another administrator demotes this caller,
    // and the next read resolves without it. The controls have to leave with it: this surface
    // withholds rather than disables, so a control left mounted here would be one the server
    // refuses, offered as though it still worked.
    it('unmounts the space-wide controls when administer authority is revoked mid-session', () => {
        const {rerender} = renderTab();

        // Live to begin with: the write reaches the hook with the whole set.
        fireEvent.click(matrixCheckbox('space-default-create_page'));
        expect(mockSetDefaults).toHaveBeenCalledWith(['edit_page']);
        expect(screen.getByRole('radio', {name: /Private/})).toBeInTheDocument();

        mockPermissionsState = {...mockPermissionsState, canAdminister: false, canManageMembers: false};
        rerender(
            <>
                <PermissionsTab
                    space={space}
                    onClose={jest.fn()}
                />
                <DocsModalController/>
            </>,
        );

        expect(document.getElementById('space-default-create_page')).toBeNull();
        expect(screen.queryByRole('radio', {name: /Private/})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Add'})).not.toBeInTheDocument();

        // The roster survives: losing the authority to change the space is not losing the
        // ability to read who is in it.
        expect(screen.getByText('People with access')).toBeInTheDocument();
    });

    // Both space-wide writes would be refused, so neither control is rendered: an affordance the
    // server would reject is withheld, never offered in a disabled state.
    it('withholds both space-wide controls from a member who cannot administer the space', () => {
        mockPermissionsState = {...mockPermissionsState, canAdminister: false, canManageMembers: false};
        renderTab();

        expect(screen.queryByRole('checkbox', {name: 'Create pages'})).not.toBeInTheDocument();
        expect(screen.queryByRole('radio', {name: /Private/})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: 'Add'})).not.toBeInTheDocument();
        expect(screen.queryByRole('button', {name: /Ada/})).not.toBeInTheDocument();

        // The roster itself still reads, so the caller learns who is in the space.
        expect(screen.getByText('People with access')).toBeInTheDocument();
    });

    // The case a single lock cannot express, and the reason the hook exposes two. A team
    // manage_space holder is admitted by the roster routes but refused by the space-wide ones, so
    // the member matrix must stay live while view_access and the default set stay locked. Gating
    // both on canAdminister showed this caller a read-only tab for work the server would accept.
    it('leaves the member matrix live for a team administrator granted only manage_space', () => {
        mockPermissionsState = {
            ...mockPermissionsState,
            canAdminister: false,
            canManageMembers: true,
            members: new Map([
                ['me', {user_id: 'me', permissions: ['read_page'], granted_permissions: [], is_admin: false, is_guest: false, is_auto_joined: false}],
                ['u2', {user_id: 'u2', permissions: ['read_page'], granted_permissions: ['edit_page'], is_admin: false, is_guest: false, is_auto_joined: false}],
            ]),
        };
        renderTab(jest.fn(), {currentUser: {id: 'me', username: 'caleb'}});

        // The space-wide knobs: withheld. Addressed by id rather than label, since the member row
        // below carries a checkbox with the same visible name.
        expect(document.getElementById('space-default-create_page')).toBeNull();
        expect(screen.queryByRole('radio', {name: /Private/})).not.toBeInTheDocument();

        // The roster: live, and a toggle reaches the hook.
        const memberEdit = matrixCheckbox('member-u2-create_page');
        expect(memberEdit.disabled).toBe(false);
        fireEvent.click(memberEdit);
        expect(mockSetMemberGrants).toHaveBeenCalledWith('u2', ['create_page', 'edit_page']);

        // Self-targeting also needs the stricter admin tier. A team administrator granted
        // manage_space can edit another person's row, but offering their own would produce a 403.
        expect(document.getElementById('member-me-create_page')).toBeNull();

        // Promoting a space administrator is a stricter operation than managing the roster. The
        // server refuses it from manage_space alone, so this cell is never rendered.
        expect(document.getElementById('member-u2-admin_space')).toBeNull();
        expect(mockSetMemberGrants).toHaveBeenCalledTimes(1);
    });

    // Until the first read resolves there is no truthful state to edit against, so the
    // controls are locked without claiming the caller lacks authority.
    it('locks the controls while permission state is still loading', () => {
        mockPermissionsState = {...mockPermissionsState, loading: true};
        renderTab();

        expect(screen.getByRole('checkbox', {name: 'Create pages'})).toBeDisabled();
        expect(screen.getByRole('radio', {name: /Private/})).toBeDisabled();
    });

    // A read that failed says so. Without this the tab is indistinguishable from one belonging to
    // a caller who simply is not an administrator: controls absent, and no reason given.
    it('blames the failed read, not the caller, when permission state could not load', () => {
        mockPermissionsState = {...mockPermissionsState, canAdminister: false, canManageMembers: false, loadFailed: true};
        renderTab();

        expect(screen.getByText("Couldn't load this space's permissions. Close and reopen to try again.")).toBeInTheDocument();
    });

    // The per-member half of the matrix. The roster mock supplies Caleb ('me') and Ada
    // ('u2'); the hook supplies what each of them holds.
    describe('the per-member permission matrix', () => {
        const withMembers = () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([
                    ['me', {user_id: 'me', permissions: ['read_page'], granted_permissions: [], is_admin: true, is_guest: false, is_auto_joined: false}],
                    ['u2', {user_id: 'u2', permissions: ['read_page', 'create_page', 'edit_page'], granted_permissions: ['edit_page'], is_admin: false, is_guest: false, is_auto_joined: false}],
                ]),
            };
        };

        it('renders a row of toggles per member, checked from their granted set', () => {
            withMembers();
            renderTab();

            expect(screen.getByRole('button', {name: 'Admin — more actions for Caleb'})).toHaveTextContent('Admin');
            expect(screen.getByRole('button', {name: 'Member — more actions for Ada'})).toHaveTextContent('Member');

            // Ada holds edit_page as a per-member grant; nobody holds delete_page.
            // Addressed by id: the label alone is ambiguous now, since the same vocabulary
            // is rendered once for the space default and once per member — which is what
            // makes this a matrix rather than a single row.
            const adaEdit = matrixCheckbox('member-u2-edit_page');
            const adaCreate = matrixCheckbox('member-u2-create_page');
            const adaDelete = matrixCheckbox('member-u2-delete_page');
            expect(adaEdit.checked).toBe(true);
            expect(adaCreate.checked).toBe(false);
            expect(adaDelete.checked).toBe(false);

            // Every permission appears once. The read baseline is not a grant anyone can make,
            // so it is stated rather than offered as a control.
            expect(document.getElementById('member-u2-read_page')).toBeNull();
            expect(document.getElementById('member-u2-read_page-inherited')).toHaveTextContent('View pages');
            expect(document.getElementById('member-u2-read_page-inherited')).toHaveTextContent('Everyone with access');

            // create_page is in the space default, so ticking it would add a grant that changes
            // nothing today — the row says so rather than leaving the reader to infer it from an
            // unticked box beside a permission the member visibly holds.
            expect(adaCreate.closest('div')).toHaveTextContent('Also from the space default');
        });

        // The write endpoint replaces granted_permissions wholesale, so the whole set goes.
        it('sends the member\'s whole granted set when one is toggled', () => {
            withMembers();
            renderTab();

            fireEvent.click(matrixCheckbox('member-u2-create_page'));

            expect(mockSetMemberGrants).toHaveBeenCalledWith('u2', ['create_page', 'edit_page']);
        });

        // The server refuses any grant to a guest, so the row says so instead of offering it.
        // A guest holds read_page and nothing else, and the server refuses every grant to one, so
        // the row states what they hold and offers no toggle to change it.
        it('offers no grants on a guest\'s row', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([
                    ['u2', {user_id: 'u2', permissions: ['read_page'], granted_permissions: [], is_admin: false, is_guest: true, is_auto_joined: false}],
                ]),
            };
            renderTab();

            expect(screen.getByRole('button', {name: 'Guest — more actions for Ada'})).toHaveTextContent('Guest');
            expect(document.getElementById('member-u2-edit_page')).toBeNull();

            // What a guest holds is still stated — read access, and its source — with nothing
            // offered to change it.
            expect(document.getElementById('member-u2-read_page-inherited')).toHaveTextContent('View pages');
        });

        // A member the permissions read did not return has no row rather than an empty one.
        it('renders no toggles for a profile the permission read did not cover', () => {
            renderTab();

            expect(document.getElementById('member-u2-edit_page')).toBeNull();
        });

        // Before an open-to-private transition prunes them, an administrator can distinguish
        // somebody who self-joined by authoring from somebody deliberately invited.
        it('marks a member the auto-join added, and leaves a deliberately added one unmarked', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([
                    ['u2', {user_id: 'u2', permissions: ['read_page'], granted_permissions: [], is_admin: false, is_guest: false, is_auto_joined: true}],
                ]),
            };
            const {unmount} = renderTab();
            expect(screen.getByText('Joined automatically by editing this space')).toBeInTheDocument();
            unmount();

            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([
                    ['u2', {user_id: 'u2', permissions: ['read_page'], granted_permissions: [], is_admin: false, is_guest: false, is_auto_joined: false}],
                ]),
            };
            renderTab();
            expect(screen.queryByText('Joined automatically by editing this space')).not.toBeInTheDocument();
        });
    });

    // The lock/reason matrix behind these actions is covered by
    // hooks/space_access_editor.test.tsx; this only confirms the tab wires the
    // hook's actions into MemberList.
    it('wires the hook\'s actions into MemberList: Remove reaches the hook, Leave closes on success', async () => {
        const onClose = jest.fn();

        renderTab(onClose, {currentUser: {id: 'me', username: 'caleb'}});

        fireEvent.click(screen.getByRole('button', {name: /Ada/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Remove from space'}));

        // The mutation waits on the confirmation.
        expect(mockRemoveMember).not.toHaveBeenCalled();

        await confirm(/Yes, remove/);

        await waitFor(() => expect(mockRemoveMember).toHaveBeenCalledWith('u2'));

        // Leaving destroys your access to what is behind this tab, so the settings
        // modal must close too rather than sit open on a space you just left.
        fireEvent.click(screen.getByRole('button', {name: /Caleb/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: 'Leave space'}));

        expect(mockLeave).not.toHaveBeenCalled();

        await confirm(/Yes, leave space/);

        await waitFor(() => expect(mockLeave).toHaveBeenCalled());
        await waitFor(() => expect(onClose).toHaveBeenCalled());
    });
});

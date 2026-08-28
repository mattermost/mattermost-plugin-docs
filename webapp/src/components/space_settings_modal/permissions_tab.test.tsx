// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {act, fireEvent, screen, waitFor} from '@testing-library/react';
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

    // Private is a real option now; it used to be disabled with a "Coming soon" reason.
    it('changes view access through the hook', () => {
        renderTab();

        fireEvent.click(screen.getByRole('radio', {name: /Private/}));

        expect(mockSetViewAccess).toHaveBeenCalledWith('private');
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

        // Every custom scheme also defines a guest role, which the server refuses to mint without
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

    // The controls stay visible for a non-admin: what the space allows is worth reading
    // even when you may not change it.
    it('locks both controls for a member who cannot administer the space', () => {
        mockPermissionsState = {...mockPermissionsState, canAdminister: false, canManageMembers: false};
        renderTab();

        expect(screen.getByRole('checkbox', {name: 'Create pages'})).toBeDisabled();
        expect(screen.getByRole('radio', {name: /Private/})).toBeDisabled();
        expect(screen.getByRole('button', {name: 'Add'})).toBeDisabled();

        // Remove is withheld entirely from a surface that cannot manage members, rather than
        // offered disabled.
        expect(screen.queryByRole('button', {name: /Ada/})).not.toBeInTheDocument();

        fireEvent.click(screen.getByRole('checkbox', {name: 'Create pages'}));
        fireEvent.click(screen.getByRole('radio', {name: /Private/}));

        expect(mockSetDefaults).not.toHaveBeenCalled();
        expect(mockSetViewAccess).not.toHaveBeenCalled();
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

        // The space-wide knobs: locked. Addressed by id rather than label, since the member row
        // below carries a checkbox with the same visible name.
        expect(document.getElementById('space-default-create_page')).toBeDisabled();
        expect(screen.getByRole('radio', {name: /Private/})).toBeDisabled();

        // The roster: live, and a toggle reaches the hook.
        const memberEdit = matrixCheckbox('member-u2-create_page');
        expect(memberEdit.disabled).toBe(false);
        fireEvent.click(memberEdit);
        expect(mockSetMemberGrants).toHaveBeenCalledWith('u2', ['create_page', 'edit_page']);

        // Self-targeting also needs the stricter admin tier. A team administrator granted
        // manage_space can edit another person's row, but offering their own would produce a 403.
        expect(matrixCheckbox('member-me-create_page').disabled).toBe(true);

        // Promoting a space administrator is a stricter operation than managing the roster. The
        // server refuses it from manage_space alone, so this one cell must not remain actionable.
        const memberAdmin = matrixCheckbox('member-u2-admin_space');
        expect(memberAdmin.disabled).toBe(true);
        fireEvent.click(memberAdmin);
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

    // A read that failed says so. Telling an administrator they are not one — which is what
    // reusing the non-admin reason here would do — is worse than saying nothing.
    it('blames the failed read, not the caller, when permission state could not load', () => {
        mockPermissionsState = {...mockPermissionsState, canAdminister: false, canManageMembers: false, loadFailed: true};
        renderTab();

        const option = screen.getByRole('radio', {name: /Private/});
        expect(option).toBeDisabled();
        expect(option).toHaveAttribute('title', "Couldn't load this space's permissions. Close and reopen to try again.");
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

            // Effective authority is shown separately from the additional-grant controls:
            // create_page comes from the space default, while edit_page is Ada's direct grant.
            const effective = screen.getByRole('group', {name: 'Effective permissions for ada'});
            expect(effective).toHaveTextContent('Effective permissions:');
            expect(effective).toHaveTextContent('View pages');
            expect(effective).toHaveTextContent('Create pages');
            expect(effective).toHaveTextContent('Edit pages');
        });

        // The write endpoint replaces granted_permissions wholesale, so the whole set goes.
        it('sends the member\'s whole granted set when one is toggled', () => {
            withMembers();
            renderTab();

            fireEvent.click(matrixCheckbox('member-u2-create_page'));

            expect(mockSetMemberGrants).toHaveBeenCalledWith('u2', ['create_page', 'edit_page']);
        });

        // The server refuses any grant to a guest, so the row says so instead of offering it.
        it('locks a guest\'s row', () => {
            mockPermissionsState = {
                ...mockPermissionsState,
                members: new Map([
                    ['u2', {user_id: 'u2', permissions: ['read_page'], granted_permissions: [], is_admin: false, is_guest: true, is_auto_joined: false}],
                ]),
            };
            renderTab();

            expect(screen.getByRole('button', {name: 'Guest — more actions for Ada'})).toHaveTextContent('Guest');
            const guestEdit = matrixCheckbox('member-u2-edit_page');
            expect(guestEdit.disabled).toBe(true);

            fireEvent.click(guestEdit);
            expect(mockSetMemberGrants).not.toHaveBeenCalled();
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

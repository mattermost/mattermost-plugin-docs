// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The AppError ids the client dispatches on, mirroring the ids the server raises. Kept in one
// place because three of these answer the same 409, so the id rather than the status is what tells
// them apart. client/rest.ts lifts the id off the wire into RestError.server_error_id.

// The space would be left with no member holding access.
export const LAST_SPACE_MEMBER_ERROR_ID = 'app.space.remove_member.last_member.app_error';

// The space would be left with members but no administrator — a different remedy from the above.
export const LAST_SPACE_ADMIN_ERROR_ID = 'app.space.member.last_admin.app_error';

// A space-keyed lock timeout. Retryable as-is, so it must not be reported as someone else having
// changed the space.
export const SPACE_LOCK_TIMEOUT_ERROR_ID = 'app.space.lock_timeout.app_error';

// A guest cannot hold a per-member grant, so the grant was refused rather than applied.
export const GUEST_NOT_ASSIGNABLE_ERROR_ID = 'app.space.member.guest_not_assignable.app_error';

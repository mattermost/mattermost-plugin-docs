// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The AppError ids the client dispatches on, mirroring the ids the server raises.
// Kept in one place because several of these answer the same HTTP status — three of
// the space-membership rules answer 409 — so the id, not the status, is what tells
// them apart, and a rename on the server has to land in exactly one place here.
// client/rest.ts lifts the id off the wire into RestError.server_error_id.

// The space would be left with no member holding access.
export const LAST_SPACE_MEMBER_ERROR_ID = 'app.space.remove_member.last_member.app_error';

// The space would be left with members but no administrator. Distinct from the above
// because the remedy differs: another admin is required, not another member.
export const LAST_SPACE_ADMIN_ERROR_ID = 'app.space.member.last_admin.app_error';

// A space-keyed lock timeout, which the server distinguishes from a stale-baseline
// conflict even though both answer 409. Retryable as-is, so it must not be reported
// as someone else having changed the space.
export const SPACE_LOCK_TIMEOUT_ERROR_ID = 'app.space.lock_timeout.app_error';

// A guest cannot hold a per-member grant, so the grant was refused rather than applied.
export const GUEST_NOT_ASSIGNABLE_ERROR_ID = 'app.space.member.guest_not_assignable.app_error';

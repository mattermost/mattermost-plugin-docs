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

// Core refuses creation or reuse of a non-preset channel scheme without this entitlement.
export const CUSTOM_PERMISSION_SCHEME_LICENSE_ERROR_ID = 'app.scheme.plugin_scheme.scheme_license.app_error';

// Core also refuses a non-preset scheme whose guest role grants anything — every Docs scheme lets
// guests read — without the guest account permissions entitlement.
export const GUEST_PERMISSIONS_LICENSE_ERROR_ID = 'app.scheme.plugin_scheme.guest_license.app_error';

// The add route answers 403 for two different rules: the target isn't an active member of the
// space's team, or the caller lacks manage authority over the space. The id is what tells them
// apart, the same way the removal routes' 409s are told apart above.
export const NOT_TEAM_MEMBER_ERROR_ID = 'app.space.member.not_team_member.app_error';

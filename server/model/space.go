// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"maps"
	"net/http"
	"strings"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

const (
	SpaceTitleMaxRunes       = 128
	SpaceDescriptionMaxRunes = 1024
	SpaceIconMaxBytes        = 256

	// SpacePropsMaxBytes caps the serialized size of the opaque Props map.
	SpacePropsMaxBytes = 64 * 1024

	// ViewAccessOpen allows the app-layer read resolver to admit eligible team non-members;
	// ViewAccessPrivate restricts ordinary reads to backing-channel members. There is no third
	// value.
	ViewAccessOpen    ViewAccess = "open"
	ViewAccessPrivate ViewAccess = "private"
)

// ViewAccess is a space's non-member read policy, one of ViewAccessOpen/ViewAccessPrivate. The
// defined type keeps the closed vocabulary visible at every call site, mirroring core's ChannelType;
// it marshals as a plain JSON string, so the wire format is a bare "open"/"private".
type ViewAccess string

// IsValid reports whether v is one of the two defined policies. The empty string is not one of
// them: PreSave deliberately does not default ViewAccess, so an unset value means the caller
// never chose one.
func (v ViewAccess) IsValid() bool {
	return v == ViewAccessOpen || v == ViewAccessPrivate
}

// Space is stored in the DOCS_Space table. Each space owns a backing MM channel (ChannelId).
// A soft-deleted space (DeleteAt>0) retains its pages; pages share the same DeleteAt via a
// cascade and can be restored with the space.
type Space struct {
	Id          string                  `json:"id"`
	ChannelId   string                  `json:"-"`
	TeamId      string                  `json:"team_id"`
	CreatorId   string                  `json:"creator_id"`
	Title       string                  `json:"title"`
	Description string                  `json:"description,omitempty"`
	Icon        string                  `json:"icon,omitempty"`
	Props       mmmodel.StringInterface `json:"props"`
	// ViewAccess is one of ViewAccessOpen/ViewAccessPrivate. It changes only the non-member branch
	// of the read resolver; member reads still pass through its other team and permission checks.
	ViewAccess ViewAccess `json:"view_access"`
	CreateAt   int64      `json:"create_at"`
	UpdateAt   int64      `json:"update_at"`
	DeleteAt   int64      `json:"delete_at"`
	SortOrder  int64      `json:"sort_order"`
}

// SpaceWithAccess carries the caller-relevant access state alongside the plain Space fields. The
// anonymous Space embed keeps the JSON flat, mirroring core's ChannelMemberWithTeamData pattern.
// DefaultPermissions is the space's default permission set, read_page-free (the implicit
// baseline). Permissions is the caller's own effective set and does include read_page, since an
// effective set states what the caller may actually do rather than what was granted on top of the
// baseline. Both are non-nil-on-empty.
//
// Permissions states what the caller may do in this space, not where the authority came from, so it
// carries manage_space when the caller holds the manage tier — whether through channel admin_space,
// a team-level grant, or being a system admin. Nothing here is scoped to the backing channel: this
// is the plugin's own answer, not a projection of a channel's permission set, and a caller's
// authority over a space legitimately arrives from outside it. SpaceMember.Permissions is the
// narrower per-member statement — what the space itself grants that member — and does not resolve
// team-level authority, which would cost a lookup per row.
//
// An endpoint returns this wrapper when it establishes access state (CreateSpace), changes it
// (SetSpaceDefaultPermissions), reads a space directly (GET /spaces/{id}), or patches one
// (PATCH /spaces/{id}): a patch may alter view_access, which moves who may read the space, and
// answering with the wrapper keeps a client refreshing its cached entry from dropping the
// permission fields. The endpoints that return a bare Space are the restore route, which alters
// neither field, and the team listing, which omits them because resolving a permission set per
// space would cost a scheme-and-role lookup per row.
//
// Because the embed is flat, a bare Space and this wrapper are indistinguishable to a client that
// types them alike: a client caching a space must merge a bare-Space response into its cached
// entry rather than replace it, or it will drop the permission fields an earlier read supplied.
type SpaceWithAccess struct {
	Space
	DefaultPermissions []string `json:"default_permissions"`
	Permissions        []string `json:"permissions"`

	// CanJoin reports that the caller may join this space themselves, which is what turns its
	// DefaultPermissions into permissions they would actually hold. Resolved by the server rather
	// than inferred by the client: a guest member and a non-member reading through the open-space
	// fall-through both carry read_page alone against the same DefaultPermissions, so the two look
	// identical on the wire; deriving CanJoin client-side risks granting a guest authoring
	// permission the server denies.
	CanJoin bool `json:"can_join"`
}

// EnsurePermissions normalizes DefaultPermissions and Permissions to non-nil slices so they
// marshal as JSON [] rather than null, mirroring the Space.GetProps discipline.
func (w *SpaceWithAccess) EnsurePermissions() {
	if w.DefaultPermissions == nil {
		w.DefaultPermissions = []string{}
	}
	if w.Permissions == nil {
		w.Permissions = []string{}
	}
}

// SpacePatch carries a partial update to a space's mutable fields. A nil field is left unchanged; a
// non-nil field (including an empty string) overwrites the current value, so a field can be cleared.
type SpacePatch struct {
	Title       *string                  `json:"title"`
	Description *string                  `json:"description"`
	Icon        *string                  `json:"icon"`
	Props       *mmmodel.StringInterface `json:"props"`
	ViewAccess  *ViewAccess              `json:"view_access"`
}

// SpaceMember is the API-facing view of a user's membership in a space. Membership is backed by
// the space's channel; this type projects the caller's effective membership state — permissions,
// admin/guest standing — while the raw channel mechanics (channel id, generated scheme-role names,
// ExplicitRoles string, notify props) stay internal.
type SpaceMember struct {
	UserId string `json:"user_id"`
	// Permissions is the member's effective permission set (space default union granted),
	// including the read_page baseline.
	Permissions []string `json:"permissions"`
	// GrantedPermissions is the member's per-member granted set beyond the space default,
	// read_page-free since the baseline is never independently granted.
	GrantedPermissions []string `json:"granted_permissions"`
	IsAdmin            bool     `json:"is_admin"`
	IsGuest            bool     `json:"is_guest"`
	// IsAutoJoined reports whether the plugin currently holds an auto-join provenance marker for the
	// member. Marker cleanup is best-effort, so it is not proof of present intent.
	IsAutoJoined bool `json:"is_auto_joined"`
}

// EnsurePermissions normalizes Permissions and GrantedPermissions to non-nil slices so they
// marshal as JSON [] rather than null, mirroring the Space.GetProps discipline.
func (m *SpaceMember) EnsurePermissions() {
	if m.Permissions == nil {
		m.Permissions = []string{}
	}
	if m.GrantedPermissions == nil {
		m.GrantedPermissions = []string{}
	}
}

// IsValid rejects a nil patch and an all-nil-fields patch — both no-ops that would otherwise bump
// UpdateAt and consume the optimistic-lock baseline without a real change. Enforced here, not just
// in the service, so callers that bypass the service still uphold it — mirroring PagePatch.IsValid.
func (p *SpacePatch) IsValid() *mmmodel.AppError {
	if p == nil || (p.Title == nil && p.Description == nil && p.Icon == nil && p.Props == nil && p.ViewAccess == nil) {
		return mmmodel.NewAppError("SpacePatch.IsValid", "model.space.patch.nothing_to_update.app_error", nil, "", http.StatusBadRequest)
	}
	return nil
}

// Patch applies the non-nil fields of patch to the space. Normalization (title trim, etc.) happens
// in PreUpdate. Callers must call patch.IsValid() first — a nil patch is a no-op here rather than a
// panic, but produces no changes, silently defeating the caller's intent.
func (s *Space) Patch(patch *SpacePatch) {
	if patch == nil {
		return
	}
	if patch.Title != nil {
		s.Title = *patch.Title
	}
	if patch.Description != nil {
		s.Description = *patch.Description
	}
	if patch.Icon != nil {
		s.Icon = *patch.Icon
	}
	if patch.Props != nil {
		s.Props = maps.Clone(*patch.Props)
	}
	if patch.ViewAccess != nil {
		s.ViewAccess = *patch.ViewAccess
	}
}

// PreSave sanitizes Space and defaults its Id-independent fields before insert.
func (s *Space) PreSave() {
	if s.Id == "" {
		s.Id = mmmodel.NewId()
	}

	s.Title = strings.TrimSpace(mmmodel.SanitizeUnicode(s.Title))
	s.Description = mmmodel.SanitizeUnicode(s.Description)
	s.Icon = mmmodel.SanitizeUnicode(s.Icon)

	if s.Props == nil {
		s.Props = make(mmmodel.StringInterface)
	}

	now := mmmodel.GetMillis()
	if s.CreateAt == 0 {
		s.CreateAt = now
	}
	s.UpdateAt = now

	if s.SortOrder == 0 {
		s.SortOrder = s.CreateAt
	}
}

// PreUpdate sanitizes Space and stamps UpdateAt before an update is persisted.
func (s *Space) PreUpdate() {
	s.UpdateAt = mmmodel.GetMillis()
	s.Title = strings.TrimSpace(mmmodel.SanitizeUnicode(s.Title))
	s.Description = mmmodel.SanitizeUnicode(s.Description)
	s.Icon = mmmodel.SanitizeUnicode(s.Icon)

	if s.Props == nil {
		s.Props = make(mmmodel.StringInterface)
	}
}

// Auditable returns Space's fields safe to include in an audit log.
func (s *Space) Auditable() map[string]any {
	return map[string]any{
		"id":          s.Id,
		"channel_id":  s.ChannelId,
		"team_id":     s.TeamId,
		"creator_id":  s.CreatorId,
		"title":       s.Title,
		"description": s.Description,
		"icon":        s.Icon,
		"props":       s.GetProps(),
		"view_access": s.ViewAccess,
		"create_at":   s.CreateAt,
		"update_at":   s.UpdateAt,
		"delete_at":   s.DeleteAt,
		"sort_order":  s.SortOrder,
	}
}

// IsValid checks Space's required fields and size limits.
func (s *Space) IsValid() *mmmodel.AppError {
	if !mmmodel.IsValidId(s.Id) {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.id.app_error", nil, "", http.StatusBadRequest)
	}

	if s.CreateAt == 0 {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.create_at.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if s.UpdateAt == 0 {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.update_at.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if !mmmodel.IsValidId(s.ChannelId) {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.channel_id.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if s.TeamId != "" && !mmmodel.IsValidId(s.TeamId) {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.team_id.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if s.CreatorId != "" && !mmmodel.IsValidId(s.CreatorId) {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.creator_id.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if strings.TrimSpace(s.Title) == "" {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.title.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if utf8.RuneCountInString(s.Title) > SpaceTitleMaxRunes {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.title_length.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if utf8.RuneCountInString(s.Description) > SpaceDescriptionMaxRunes {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.description_length.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if len(s.Icon) > SpaceIconMaxBytes {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.icon_length.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	if err := ValidatePropsSize("Space.IsValid", "id="+s.Id, s.Props, SpacePropsMaxBytes); err != nil {
		return err
	}

	if !s.ViewAccess.IsValid() {
		return mmmodel.NewAppError("Space.IsValid", "model.space.is_valid.view_access.app_error", nil, "id="+s.Id, http.StatusBadRequest)
	}

	return nil
}

// GetProps returns Props, or an empty map if Props is nil.
func (s *Space) GetProps() mmmodel.StringInterface {
	return ensureProps(s.Props)
}

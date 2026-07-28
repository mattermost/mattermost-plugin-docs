// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package app provides the business-logic service layer for the Docs plugin,
// sitting between the HTTP API and the plugin store. Store errors are wrapped into
// *mmmodel.AppError so HTTP status codes reach the client.
package app

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// Logger is the logging surface the service needs. Info exists for the operator-facing audit lines
// the importer must emit (plan §24: upload accepted/rejected, import started/finished, cleanup
// counts) — those must be visible without turning on debug logging, unlike the Debug tracing used
// elsewhere in the service.
type Logger interface {
	Debug(msg string, keyValuePairs ...any)
	Info(msg string, keyValuePairs ...any)
	Warn(msg string, keyValuePairs ...any)
	Error(msg string, keyValuePairs ...any)
}

type noopLogger struct{}

func (noopLogger) Debug(_ string, _ ...any) {}
func (noopLogger) Info(_ string, _ ...any)  {}
func (noopLogger) Warn(_ string, _ ...any)  {}
func (noopLogger) Error(_ string, _ ...any) {}

// Service is the central service struct for the Docs plugin.
type Service struct {
	store  *store.Store
	log    Logger
	client *pluginapi.Client

	// presenceBroadcastTimes records the last channel-wide presence broadcast time (ms) per
	// (pageID, userID). Autosave cadence is client-driven and unbounded server-side, and every autosave
	// on a live page would otherwise fan a presence event out to the whole channel, so this caps those
	// broadcasts to at most one per presenceBroadcastMinIntervalMs per (page, user). Delete and publish
	// paths bypass this and always broadcast.
	//
	// The map is per-process, so each node throttles independently: a user whose autosaves are
	// spread across nodes can broadcast more often than the interval implies. That is acceptable —
	// the payload is queried fresh from the shared DB on every broadcast, so the throttle only
	// trades broadcast volume, never correctness. Entries are dropped on discard and publish, and
	// swept by age via sweepPresenceBroadcastTimes for sessions abandoned without either.
	presenceBroadcastTimes sync.Map

	// lastPresenceSweepAt is the timestamp (ms) of the most recent presenceBroadcastTimes sweep, used
	// to rate-limit the sweep itself to once per presenceBroadcastSweepIntervalMs.
	lastPresenceSweepAt atomic.Int64
}

// New creates a Service wired to the given store, logger, and optional pluginapi client.
// Passing nil for store panics immediately; passing nil for log installs a no-op logger.
// client may be nil: WS publish methods become no-ops, and channel/team-backed operations
// (membership checks, member management, space listing) return a client-not-wired error.
func New(s *store.Store, log Logger, client *pluginapi.Client) *Service {
	if s == nil {
		panic("app.New: store must not be nil")
	}
	if log == nil {
		log = noopLogger{}
	}
	return &Service{
		store:  s,
		log:    log,
		client: client,
	}
}

// validateTitle sanitizes and validates an entity title, returning the normalized form.
// where identifies the calling operation for logs; the message keys are shared across callers.
func validateTitle(where, title string, maxRunes int) (string, *mmmodel.AppError) {
	title = normalizeTitle(title)
	if title == "" {
		return "", mmmodel.NewAppError(where, "app.shared.title_required.app_error", nil, "", http.StatusBadRequest)
	}
	if utf8.RuneCountInString(title) > maxRunes {
		return "", mmmodel.NewAppError(where, "app.shared.title_too_long.app_error", map[string]any{"MaxLength": maxRunes}, "", http.StatusBadRequest)
	}
	return title, nil
}

func normalizeTitle(title string) string {
	return strings.TrimSpace(mmmodel.SanitizeUnicode(title))
}

// normalizeAndValidatePagePatch normalizes a page update patch's Title (trimmed, empty rejected),
// with the result written back into the patch; Body and SearchText are left as-is. A nil field
// means "leave unchanged". It defers patch-shape validation to PagePatch.IsValid.
func normalizeAndValidatePagePatch(where string, patch *model.PagePatch) *mmmodel.AppError {
	// The patch.Title != nil guard below protects the title dereference; IsValid only
	// rejects a nil or all-nil patch and can pass with Title == nil.
	if validErr := patch.IsValid(); validErr != nil {
		return validErr
	}
	if patch.Title != nil {
		normalized, titleErr := validateTitle(where, *patch.Title, model.PageTitleMaxRunes)
		if titleErr != nil {
			return titleErr
		}
		patch.Title = &normalized
	}
	return nil
}

// requireBaseline rejects a mutation that supplies neither an optimistic-lock baseline nor
// force. SafeDereference would otherwise turn an absent baseline into 0, which never matches a
// live row's timestamp, so every such request would fail as a misleading "changed by someone
// else" conflict; requiring the field makes the contract explicit instead. field names the
// caller-facing JSON field for the error message.
func requireBaseline(where, field string, baseline *int64, force bool) *mmmodel.AppError {
	if baseline == nil && !force {
		return mmmodel.NewAppError(where, "app.optimistic_lock.baseline_required.app_error", map[string]any{"Field": field}, "", http.StatusBadRequest)
	}
	return nil
}

// storeAppError maps a store sentinel error to an *AppError with the conventional status code
// and a shared message key (app.store.*); the where argument identifies the calling operation for logs.
// This is the default for translating store errors; hand-roll an inline NewAppError only when a
// case needs a message key or metadata this can't produce (e.g. CreatePage's space-not-found, or
// UpdatePage's conflict carrying ModifiedBy/ModifiedAt).
func storeAppError(where string, err error) *mmmodel.AppError {
	switch {
	case store.IsErrNotFound(err):
		return mmmodel.NewAppError(where, "app.store.not_found.app_error", nil, "", http.StatusNotFound).Wrap(err)
	case store.IsErrCircularReference(err):
		return mmmodel.NewAppError(where, "app.page.circular_reference.app_error", nil, "", http.StatusBadRequest).Wrap(err)
	case store.IsErrInvalidInput(err):
		return invalidInputAppError(where, err)
	case store.IsErrConflict(err):
		return mmmodel.NewAppError(where, "app.store.conflict.app_error", nil, "", http.StatusConflict).Wrap(err)
	case store.IsErrLimitExceeded(err):
		// Use the limit the error carries; different store methods have different bounds.
		var limitErr *store.ErrLimitExceeded
		_ = errors.As(err, &limitErr) // guaranteed true: IsErrLimitExceeded already performed this assertion
		switch limitErr.Reason {
		case store.ReasonMaxDepthExceeded:
			return mmmodel.NewAppError(where, "app.page.max_depth_exceeded.app_error", map[string]any{"MaxDepth": limitErr.Limit}, "", http.StatusBadRequest).Wrap(err)
		case store.ReasonSubtreeMaxDepthExceeded:
			return mmmodel.NewAppError(where, "app.page.subtree_max_depth_exceeded.app_error", map[string]any{"MaxDepth": limitErr.Limit}, "", http.StatusBadRequest).Wrap(err)
		case store.ReasonDraftQuotaExceeded:
			// 422, not 429: the caller is over a standing per-space draft cap, not sending too many
			// requests. Retrying the same request never succeeds until a draft is discarded, so a
			// rate-limit code would send clients into a wait-and-retry loop.
			return mmmodel.NewAppError(where, "app.page_draft.quota_exceeded.app_error", nil, "", http.StatusUnprocessableEntity).Wrap(err)
		}
		return mmmodel.NewAppError(where, "app.store.too_large.app_error", map[string]any{"Limit": limitErr.Limit}, "", http.StatusUnprocessableEntity).Wrap(err)
	default:
		return mmmodel.NewAppError(where, "app.store.internal_error.app_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}
}

// invalidInputAppError maps a store ErrInvalidInput to a 400 *AppError, preferring the
// specific validation key the store carried (from a model IsValid check) over a shared fallback.
// Reason* codes are translated to their shared message keys here; callers wanting an
// operation-specific key for a code map it themselves before falling back to this.
func invalidInputAppError(where string, err error) *mmmodel.AppError {
	var invErr *store.ErrInvalidInput
	if errors.As(err, &invErr) && invErr.Reason != "" {
		switch invErr.Reason {
		case store.ReasonParentNotLive:
			// Same key as the app-layer parent pre-checks (validateParentExists), so a parent
			// that disappears between the pre-check and the store's locked check reads
			// identically to one that never existed — the contract is not race-dependent.
			return mmmodel.NewAppError(where, "app.page.invalid_parent.app_error", nil, "", http.StatusBadRequest).Wrap(err)
		case store.ReasonDraftCycle:
			return mmmodel.NewAppError(where, "app.page_draft.update.parent_cycle.app_error", nil, "", http.StatusBadRequest).Wrap(err)
		case store.ReasonDraftTooDeep:
			return mmmodel.NewAppError(where, "app.page_draft.update.parent_too_deep.app_error", nil, "", http.StatusBadRequest).Wrap(err)
		}
		return mmmodel.NewAppError(where, invErr.Reason, nil, "", http.StatusBadRequest).Wrap(err)
	}
	return mmmodel.NewAppError(where, "app.store.invalid_input.app_error", nil, "", http.StatusBadRequest).Wrap(err)
}

// restoreReasonAppError maps a store.Reason* restore-failure code (see RestorePage, RestoreSpace)
// to a pre-built AppError via appErrors, so the store communicates which condition failed without
// naming the app-facing message key itself. Callers construct AppErrors with string-literal IDs so
// the i18n extraction tool can discover them. Returns nil if err is not an ErrInvalidInput carrying
// one of the mapped reasons, leaving the caller to fall back to storeAppError.
func restoreReasonAppError(err error, appErrors map[string]*mmmodel.AppError) *mmmodel.AppError {
	var invErr *store.ErrInvalidInput
	if !errors.As(err, &invErr) {
		return nil
	}
	appErr, ok := appErrors[invErr.Reason]
	if !ok {
		return nil
	}
	return appErr.Wrap(err)
}

// readBackAfterRestore re-reads an entity whose restore has already committed, retrying once in
// case of a transient read error. The read's own error is never surfaced: a 404/500 there would
// misreport an already committed restore as failed, prompting a retry that then 409s. The
// distinct readFailedErr tells the caller the restore succeeded and only the read-back failed.
// Callers construct readFailedErr with a string-literal ID so the i18n extraction tool can
// discover it.
func readBackAfterRestore[T any](readFailedErr *mmmodel.AppError, get func() (T, *mmmodel.AppError)) (T, *mmmodel.AppError) {
	got, getErr := get()
	if getErr != nil {
		got, getErr = get()
		if getErr != nil {
			var zero T
			return zero, readFailedErr.Wrap(getErr)
		}
	}
	return got, nil
}

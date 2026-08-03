// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

// PageMaximum caps the zero-based "page" pagination index so page*perPage can never overflow
// into a negative offset, regardless of the caller.
const PageMaximum = 1 << 20

// ClampPage normalizes a requested page index into [0, PageMaximum].
func ClampPage(page int) int {
	return min(max(page, 0), PageMaximum)
}

// PerPageDefault is the page size used when perPage is not a positive value, matching
// core's page-param convention (server/channels/web/params.go).
const PerPageDefault = 60

// PerPageMaximum is the largest page size a caller may request; larger values are
// clamped down, matching core's page-param convention.
const PerPageMaximum = 200

// ClampPerPage normalizes a requested page size: non-positive values default to PerPageDefault
// and values above PerPageMaximum are capped. The result is always in [1, PerPageMaximum].
func ClampPerPage(perPage int) int {
	if perPage <= 0 {
		return PerPageDefault
	}
	if perPage > PerPageMaximum {
		return PerPageMaximum
	}
	return perPage
}

// paginationOffsetLimit converts a zero-based page/size into an offset/limit. page is clamped
// into [0, PageMaximum] and perPage into [1, PerPageMaximum] (ClampPage/ClampPerPage), so the
// multiplication is overflow-safe for any caller, not just the HTTP layer.
// The returned limit is perPage+1: the store fetches one probe row beyond the page, and
// trimPage converts its presence into the has-more signal.
func paginationOffsetLimit(page, perPage int) (offset, limit int) {
	page = ClampPage(page)
	perPage = ClampPerPage(perPage)
	return page * perPage, perPage + 1
}

// trimPage converts a probe read of up to limit rows (see paginationOffsetLimit) into the page
// to return plus whether more rows exist: the probe row's presence is the has-more signal, and
// it is trimmed off.
func trimPage[T any](items []T, limit int) ([]T, bool) {
	if len(items) >= limit {
		return items[:limit-1], true
	}
	return items, false
}

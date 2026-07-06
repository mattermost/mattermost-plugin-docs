// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPaginationOffsetLimit verifies perPage <= 0 defaults to PerPageDefault and
// perPage > PerPageMaximum is capped at PerPageMaximum, so a caller can never
// request an unbounded result — matching core's page-param convention.
func TestPaginationOffsetLimit(t *testing.T) {
	tests := []struct {
		name          string
		page, perPage int
		wantOffset    int
		wantLimit     int
	}{
		{"zero perPage defaults", 0, 0, 0, PerPageDefault},
		{"negative perPage defaults", 0, -5, 0, PerPageDefault},
		{"perPage within range is unchanged", 1, 25, 25, 25},
		{"perPage over max is capped", 0, PerPageMaximum + 50, 0, PerPageMaximum},
		{"negative page treated as zero", -1, 10, 0, 10},
		{"offset derived from page * clamped perPage", 2, 25, 50, 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset, limit := paginationOffsetLimit(tt.page, tt.perPage)
			require.Equal(t, tt.wantOffset, offset)
			require.Equal(t, tt.wantLimit, limit)
		})
	}
}

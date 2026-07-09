// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPaginationOffsetLimit verifies perPage <= 0 defaults to PerPageDefault and
// perPage > PerPageMaximum is capped at PerPageMaximum. The returned limit is always
// perPage+1 so callers can detect has_more without a separate COUNT query.
func TestPaginationOffsetLimit(t *testing.T) {
	tests := []struct {
		name          string
		page, perPage int
		wantOffset    int
		wantLimit     int
	}{
		{"zero perPage defaults", 0, 0, 0, PerPageDefault + 1},
		{"negative perPage defaults", 0, -5, 0, PerPageDefault + 1},
		{"perPage within range is unchanged", 1, 25, 25, 26},
		{"perPage over max is capped", 0, PerPageMaximum + 50, 0, PerPageMaximum + 1},
		{"negative page treated as zero", -1, 10, 0, 11},
		{"offset derived from page * clamped perPage", 2, 25, 50, 26},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset, limit := paginationOffsetLimit(tt.page, tt.perPage)
			require.Equal(t, tt.wantOffset, offset)
			require.Equal(t, tt.wantLimit, limit)
		})
	}
}

// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPageParam(t *testing.T) {
	cases := map[string]int{
		"":         0,
		"page=3":   3,
		"page=0":   0,
		"page=-1":  0,
		"page=abc": 0,
		"page=2.5": 0,
	}
	for query, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		require.Equal(t, want, pageParam(r), query)
	}
}

func TestPerPageParam(t *testing.T) {
	cases := map[string]int{
		"":             60, // default
		"per_page=10":  10,
		"per_page=200": 200,
		"per_page=999": 200, // clamped to max
		"per_page=0":   60,  // non-positive -> default
		"per_page=-5":  60,
		"per_page=abc": 60, // unparseable -> default
	}
	for query, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		require.Equal(t, want, perPageParam(r), query)
	}
}

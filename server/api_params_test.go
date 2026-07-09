// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeJSONBody_TrailingDataRejected(t *testing.T) {
	// A valid first JSON object followed by a second value is treated as a malformed body.
	body := strings.NewReader(`{"foo":"bar"}{"extra":true}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	var v struct {
		Foo string `json:"foo"`
	}
	ok := decodeJSONBody(w, req, 1024, &v, "test", false)
	require.False(t, ok)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDecodeJSONBody_ValidConsumed(t *testing.T) {
	body := strings.NewReader(`{"foo":"bar"}`)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	var v struct {
		Foo string `json:"foo"`
	}
	ok := decodeJSONBody(w, req, 1024, &v, "test", false)
	require.True(t, ok)
	require.Equal(t, "bar", v.Foo)
}

func TestDecodeJSONBody_WhitespaceTrailing(t *testing.T) {
	body := strings.NewReader("{\"foo\":\"bar\"} \n\t")
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	var v struct {
		Foo string `json:"foo"`
	}
	ok := decodeJSONBody(w, req, 1024, &v, "test", false)
	require.True(t, ok)
}

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

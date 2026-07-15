// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

func TestValidateAndNormalizeContent(t *testing.T) {
	t.Run("empty content is a no-op", func(t *testing.T) {
		body, search, err := validateAndNormalizeContent("")
		require.NoError(t, err)
		require.Equal(t, "", body)
		require.Equal(t, "", search)
	})

	t.Run("TipTap JSON is normalized and search text derived", func(t *testing.T) {
		body, search, err := validateAndNormalizeContent(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello world"}]}]}`)
		require.NoError(t, err)
		require.Contains(t, body, "hello world")
		require.Equal(t, "hello world", search)
	})

	t.Run("plain text is wrapped into a TipTap doc", func(t *testing.T) {
		body, search, err := validateAndNormalizeContent("just text")
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(body, `{"type":"doc"`), "plain text should be wrapped: %s", body)
		require.Contains(t, body, "just text")
		require.Equal(t, "just text", search)
	})

	t.Run("malformed TipTap JSON is rejected", func(t *testing.T) {
		_, _, err := validateAndNormalizeContent(`{"type":"bogus"}`)
		require.Error(t, err)
	})

	t.Run("javascript URL is stripped on normalization", func(t *testing.T) {
		body, _, err := validateAndNormalizeContent(`{"type":"doc","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}`)
		require.NoError(t, err)
		require.NotContains(t, body, "javascript:alert")
	})

	t.Run("string starting with { but not valid JSON is wrapped as plain text", func(t *testing.T) {
		body, search, err := validateAndNormalizeContent("{shrug}")
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(body, `{"type":"doc"`), "brace-leading non-JSON must be wrapped as plain text: %s", body)
		require.Equal(t, "{shrug}", search)
	})

	t.Run("multiline plain text becomes multiple paragraphs", func(t *testing.T) {
		body, _, err := validateAndNormalizeContent("line one\nline two")
		require.NoError(t, err)
		require.Contains(t, body, "line one")
		require.Contains(t, body, "line two")
	})

	t.Run("plain text preserves leading whitespace within lines", func(t *testing.T) {
		body, _, err := validateAndNormalizeContent("  indented line")
		require.NoError(t, err)
		require.Contains(t, body, `"  indented line"`, "leading spaces must not be stripped from paragraph text")
	})
}

func TestNormalizePatchContent(t *testing.T) {
	t.Run("nil patch is a no-op", func(t *testing.T) {
		require.Nil(t, normalizePatchContent("test", nil))
	})

	t.Run("patch with nil Body is a no-op", func(t *testing.T) {
		patch := &model.PagePatch{Title: mmmodel.NewPointer("title")}
		require.Nil(t, normalizePatchContent("test", patch))
		require.Nil(t, patch.SearchText, "SearchText must remain nil when Body is unset")
	})

	t.Run("patch with valid Body normalizes and derives SearchText", func(t *testing.T) {
		body := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]}`
		patch := &model.PagePatch{Body: mmmodel.NewPointer(body)}
		require.Nil(t, normalizePatchContent("test", patch))
		require.NotNil(t, patch.SearchText)
		require.Equal(t, "hello", *patch.SearchText)
	})

	t.Run("patch with invalid Body returns 400 AppError", func(t *testing.T) {
		patch := &model.PagePatch{Body: mmmodel.NewPointer(`{"type":"bogus"}`)}
		appErr := normalizePatchContent("test", patch)
		require.NotNil(t, appErr)
		require.Equal(t, 400, appErr.StatusCode)
	})

	t.Run("patch with Body strips dangerous URLs", func(t *testing.T) {
		body := `{"type":"doc","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}`
		patch := &model.PagePatch{Body: mmmodel.NewPointer(body)}
		require.Nil(t, normalizePatchContent("test", patch))
		require.NotContains(t, *patch.Body, "javascript:alert")
	})
}

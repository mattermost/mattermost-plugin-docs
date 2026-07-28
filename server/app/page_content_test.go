// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"net/http"
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

func TestNormalizePageContent(t *testing.T) {
	t.Run("empty content is a no-op", func(t *testing.T) {
		body, search, appErr := normalizePageContent("Test", "")
		require.Nil(t, appErr)
		require.Equal(t, "", body)
		require.Equal(t, "", search)
	})

	t.Run("TipTap JSON is normalized and search text derived", func(t *testing.T) {
		body, search, appErr := normalizePageContent("Test", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello world"}]}]}`)
		require.Nil(t, appErr)
		require.Contains(t, body, "hello world")
		require.Equal(t, "hello world", search)
	})

	t.Run("plain text is wrapped into a TipTap doc", func(t *testing.T) {
		body, search, appErr := normalizePageContent("Test", "just text")
		require.Nil(t, appErr)
		require.True(t, strings.HasPrefix(body, `{"type":"doc"`), "plain text should be wrapped: %s", body)
		require.Contains(t, body, "just text")
		require.Equal(t, "just text", search)
	})

	t.Run("malformed TipTap JSON is rejected", func(t *testing.T) {
		_, _, appErr := normalizePageContent("Test", `{"type":"bogus"}`)
		require.NotNil(t, appErr)
	})

	t.Run("javascript URL is stripped on normalization", func(t *testing.T) {
		body, _, appErr := normalizePageContent("Test", `{"type":"doc","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}`)
		require.Nil(t, appErr)
		require.NotContains(t, body, "javascript:alert")
	})

	t.Run("string starting with { but not valid JSON is wrapped as plain text", func(t *testing.T) {
		body, search, appErr := normalizePageContent("Test", "{shrug}")
		require.Nil(t, appErr)
		require.True(t, strings.HasPrefix(body, `{"type":"doc"`), "brace-leading non-JSON must be wrapped as plain text: %s", body)
		require.Equal(t, "{shrug}", search)
	})

	t.Run("multiline plain text becomes multiple paragraphs", func(t *testing.T) {
		body, _, appErr := normalizePageContent("Test", "line one\nline two")
		require.Nil(t, appErr)
		require.Contains(t, body, "line one")
		require.Contains(t, body, "line two")
	})

	t.Run("plain text preserves leading whitespace within lines", func(t *testing.T) {
		body, _, appErr := normalizePageContent("Test", "  indented line")
		require.Nil(t, appErr)
		require.Contains(t, body, `"  indented line"`, "leading spaces must not be stripped from paragraph text")
	})
}

// TestNormalizePageContentRejectsTooManyParagraphs verifies that a plain-text body exceeding
// maxPlainTextParagraphs newlines is rejected with 400, rather than producing an oversized TipTap
// document.
func TestNormalizePageContentRejectsTooManyParagraphs(t *testing.T) {
	body := strings.Repeat("x\n", maxPlainTextParagraphs+1)

	_, _, appErr := normalizePageContent("test", body)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	require.Contains(t, appErr.Error(), "too many paragraphs")
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

// TestNormalizeContentBody covers the draft autosave entry point, the only one that sanitizes a body
// without deriving SearchText — publish derives it later from the same content.
func TestNormalizeContentBody(t *testing.T) {
	t.Run("an empty body is left alone", func(t *testing.T) {
		body, appErr := normalizeContentBody("test", "")
		require.Nil(t, appErr)
		require.Empty(t, body)
	})

	t.Run("valid TipTap content is normalized", func(t *testing.T) {
		body, appErr := normalizeContentBody("test",
			`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]}`)
		require.Nil(t, appErr)
		require.Contains(t, body, "hello")
	})

	t.Run("dangerous URLs are stripped", func(t *testing.T) {
		body, appErr := normalizeContentBody("test",
			`{"type":"doc","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}`)
		require.Nil(t, appErr)
		require.NotContains(t, body, "javascript:alert")
	})

	t.Run("an unlisted node type is rejected", func(t *testing.T) {
		_, appErr := normalizeContentBody("test", `{"type":"doc","content":[{"type":"script"}]}`)
		require.NotNil(t, appErr)
		require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
	})
}

func TestNormalizeContentRejectsOversizedBodyBeforeParsing(t *testing.T) {
	// The size gate must run before json.Unmarshal, so the stored-body limit — not the larger
	// request-transport cap — bounds the parse allocation.
	oversized := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` +
		strings.Repeat("a", model.PageBodyMaxBytes) + `"}]}]}`
	_, _, appErr := normalizePageContent("TestNormalizeContent", oversized)
	require.NotNil(t, appErr)
	require.Equal(t, http.StatusBadRequest, appErr.StatusCode)
}

// TestNormalizePageContentAcceptsMaxParagraphs pins the paragraph cap's boundary: a body producing
// exactly maxPlainTextParagraphs paragraphs is accepted — the cap rejects strictly more than the
// documented limit, not "the limit or more".
func TestNormalizePageContentAcceptsMaxParagraphs(t *testing.T) {
	// "x\n" repeated N-1 times splits into N-1 "x" paragraphs plus one trailing empty paragraph:
	// exactly maxPlainTextParagraphs.
	body := strings.Repeat("x\n", maxPlainTextParagraphs-1)

	_, _, appErr := normalizePageContent("test", body)
	require.Nil(t, appErr)
}

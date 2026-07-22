// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// normalizePageContent validates and normalizes a page body, deriving SearchText from it. Returns a
// 400 AppError when the body is not valid TipTap content.
//
// An empty body ("") is returned as-is, representing "no content"; the draft path instead seeds new
// pages with model.EmptyTipTapJSON (a rendered-empty document). These are two DISTINCT empty
// representations, and consumers may assign them different meaning: the publish path treats "" as
// "field not sent, preserve the existing page body" and EmptyTipTapJSON as "explicitly cleared".
func normalizePageContent(where, body string) (normBody, searchText string, appErr *mmmodel.AppError) {
	normBody, searchText, err := validateAndNormalizeContent(body)
	if err != nil {
		return "", "", mmmodel.NewAppError(where, "app.page.invalid_content.app_error", nil, "", http.StatusBadRequest).Wrap(err)
	}
	return normBody, searchText, nil
}

// normalizePatchContent normalizes a page patch's Body in place when present, recomputing
// SearchText from it (SearchText is the body's server-derived projection). A patch that sets Body
// has its caller-supplied SearchText overwritten by the derived value; a patch that does not touch
// Body is left unchanged (a SearchText-without-Body patch is rejected later by PagePatch.IsValid). A
// nil patch is a no-op, matching PagePatch's own contract.
func normalizePatchContent(where string, patch *model.PagePatch) *mmmodel.AppError {
	if patch == nil || patch.Body == nil {
		return nil
	}
	normBody, searchText, appErr := normalizePageContent(where, *patch.Body)
	if appErr != nil {
		return appErr
	}
	patch.Body = &normBody
	patch.SearchText = &searchText
	return nil
}

// validateAndNormalizeContent validates and normalizes TipTap/plain-text page content.
// Returns (normalizedBody, searchText, error). An empty content string is returned as-is (no-op).
func validateAndNormalizeContent(content string) (normBody, searchText string, err error) {
	if content == "" {
		return content, "", nil
	}
	// Treat the body as TipTap only when it is actually valid JSON: a plain-text body that merely
	// starts with "{" (e.g. "{shrug}") is not JSON and must be wrapped, not rejected. A body that is
	// valid JSON but not a "doc" is a genuine content error and ParseTipTapDocument rejects it.
	idx := strings.IndexFunc(content, func(r rune) bool { return !unicode.IsSpace(r) })
	if idx >= 0 && content[idx] == '{' {
		doc, parseErr := model.ParseTipTapDocument(content)
		if parseErr == nil {
			return marshalTipTapDoc(doc)
		}
		err = parseErr
		var syntaxErr *json.SyntaxError
		if !errors.As(err, &syntaxErr) {
			return "", "", err
		}
		// SyntaxError → not valid JSON → fall through to plain-text wrapping.
	}
	// Non-JSON content: wrap in a minimal TipTap doc.
	doc, err := convertPlainTextToTipTap(content)
	if err != nil {
		return "", "", err
	}
	return marshalTipTapDoc(doc)
}

// marshalTipTapDoc serializes a TipTapDocument and derives its search text.
func marshalTipTapDoc(doc model.TipTapDocument) (string, string, error) {
	sanitized, err := json.Marshal(doc)
	if err != nil {
		return "", "", err
	}
	return string(sanitized), model.BuildSearchText(doc), nil
}

// maxPlainTextParagraphs caps the number of paragraph nodes produced when converting plain text
// to a TipTap document. convertPlainTextToTipTap splits on newlines and turns each line into its
// own paragraph node (a map[string]any), so a newline-only body at PageBodyMaxBytes would
// otherwise build ~2M such maps before the post-normalization body-size check could reject it.
const maxPlainTextParagraphs = 10_000

// convertPlainTextToTipTap wraps plain text in a minimal TipTap document. Returns an error when
// the input has more than maxPlainTextParagraphs newlines so that over-limit bodies are rejected
// with a clear error rather than silently truncated.
func convertPlainTextToTipTap(plainText string) (model.TipTapDocument, error) {
	paragraphs := strings.SplitN(plainText, "\n", maxPlainTextParagraphs+1)
	if len(paragraphs) > maxPlainTextParagraphs {
		return model.TipTapDocument{}, errors.New("plain text body has too many paragraphs")
	}
	nodes := make([]map[string]any, 0, len(paragraphs))
	for _, para := range paragraphs {
		node := map[string]any{"type": "paragraph"}
		if strings.TrimSpace(para) != "" {
			node["content"] = []any{map[string]any{"type": "text", "text": para}}
		}
		nodes = append(nodes, node)
	}
	return model.TipTapDocument{
		Type:    model.TipTapDocType,
		Content: nodes,
	}, nil
}

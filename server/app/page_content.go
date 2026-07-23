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

// normalizePageContent normalizes a page body — validating it as TipTap content and deriving
// SearchText from it. Returns a 400 AppError when the body is not valid TipTap content.
//
// An empty body ("") is returned as-is, representing "no content"; the draft path instead seeds new
// pages with model.EmptyTipTapJSON (a rendered-empty document). These are two DISTINCT empty
// representations, and consumers may assign them different meaning: the publish path treats "" as
// "field not sent, preserve the existing page body" and EmptyTipTapJSON as "explicitly cleared".
func normalizePageContent(where, body string) (normBody, searchText string, appErr *mmmodel.AppError) {
	normBody, doc, empty, err := normalizeContent(body)
	if err != nil {
		return "", "", wrapContentError(where, err)
	}
	if !empty {
		searchText = model.BuildSearchText(doc)
	}
	return normBody, searchText, nil
}

// wrapContentError renders a content validation/normalization failure as the shared invalid-content
// AppError, so the error key and status stay defined in one place across content callers.
func wrapContentError(where string, err error) *mmmodel.AppError {
	return mmmodel.NewAppError(where, "app.page.invalid_content.app_error", nil, "", http.StatusBadRequest).Wrap(err)
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

// normalizeContentBody normalizes a body without deriving SearchText, for callers (draft autosave)
// that store only the body. It shares normalizeContent with normalizePageContent but discards the
// parsed doc, so it skips the full-text BuildSearchText walk that normalizeContent's caller would
// otherwise run on every call — a waste on the highest-frequency write path.
func normalizeContentBody(where, body string) (string, *mmmodel.AppError) {
	normBody, _, _, err := normalizeContent(body)
	if err != nil {
		return "", wrapContentError(where, err)
	}
	return normBody, nil
}

// normalizeContent normalizes TipTap/plain-text page content to its stored body form, returning the
// parsed doc and empty flag so callers decide whether to derive SearchText (normalizePageContent
// does; normalizeContentBody skips it). Returns a raw error; callers wrap it via wrapContentError.
// An empty content string ("") is returned as-is (no-op), with a zero-value doc.
func normalizeContent(content string) (normBody string, doc model.TipTapDocument, empty bool, err error) {
	doc, empty, err = normalizeContentToDoc(content)
	if err != nil {
		return "", model.TipTapDocument{}, false, err
	}
	if empty {
		return content, doc, true, nil
	}
	normBody, err = marshalTipTapDoc(doc)
	if err != nil {
		return "", model.TipTapDocument{}, false, err
	}
	return normBody, doc, false, nil
}

// normalizeContentToDoc normalizes content and returns its TipTap document, validating it and
// rejecting invalid TipTap. empty is true for an empty content string ("") — a no-op the caller
// returns as-is.
func normalizeContentToDoc(content string) (doc model.TipTapDocument, empty bool, err error) {
	if content == "" {
		return model.TipTapDocument{}, true, nil
	}
	// Treat the body as TipTap only when it is actually valid JSON: a plain-text body that merely
	// starts with "{" (e.g. "{shrug}") is not JSON and must be wrapped, not rejected. A body that is
	// valid JSON but not a "doc" is a genuine content error and ParseTipTapDocument rejects it.
	idx := strings.IndexFunc(content, func(r rune) bool { return !unicode.IsSpace(r) })
	if idx >= 0 && content[idx] == '{' {
		parsed, parseErr := model.ParseTipTapDocument(content)
		if parseErr == nil {
			return parsed, false, nil
		}
		var syntaxErr *json.SyntaxError
		if !errors.As(parseErr, &syntaxErr) {
			return model.TipTapDocument{}, false, parseErr
		}
		// SyntaxError → not valid JSON → fall through to plain-text wrapping.
	}
	// Non-JSON content: wrap in a minimal TipTap doc.
	wrapped, err := convertPlainTextToTipTap(content)
	if err != nil {
		return model.TipTapDocument{}, false, err
	}
	return wrapped, false, nil
}

// marshalTipTapDoc serializes a TipTapDocument to its stored JSON form.
func marshalTipTapDoc(doc model.TipTapDocument) (string, error) {
	sanitized, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(sanitized), nil
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

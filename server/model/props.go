// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"encoding/json"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// ensureProps returns props, or a new empty map when props is nil.
func ensureProps(props mmmodel.StringInterface) mmmodel.StringInterface {
	if props == nil {
		return make(mmmodel.StringInterface)
	}
	return props
}

// validatePropsSize enforces the serialized-size cap on a Props map.
// keyPrefix is the i18n message-key namespace (e.g. "model.page.is_valid").
func validatePropsSize(where, keyPrefix, details string, props mmmodel.StringInterface, maxBytes int) *mmmodel.AppError {
	if props == nil {
		return nil
	}
	b, err := json.Marshal(props)
	if err != nil {
		return mmmodel.NewAppError(where, keyPrefix+".props.app_error", nil, details, http.StatusBadRequest)
	}
	if len(b) > maxBytes {
		return mmmodel.NewAppError(where, keyPrefix+".props_size.app_error", map[string]any{"MaxBytes": maxBytes}, details, http.StatusBadRequest)
	}
	return nil
}

// deepCloneStringInterface returns a deep copy of a StringInterface,
// recursively copying nested maps and slices to avoid aliasing.
func deepCloneStringInterface(src mmmodel.StringInterface) mmmodel.StringInterface {
	dst := make(mmmodel.StringInterface, len(src))
	for k, v := range src {
		dst[k] = deepCloneAny(v)
	}
	return dst
}

func deepCloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = deepCloneAny(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = deepCloneAny(vv)
		}
		return out
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out
	default:
		return x
	}
}

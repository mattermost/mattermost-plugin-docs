// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model

import (
	"encoding/json"
	"maps"
	"net/http"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
)

// ensureProps returns a shallow clone of props, or a new empty map when props is nil.
// Callers may add or remove keys without affecting the source; mutations of the values themselves still alias the source.
func ensureProps(props mmmodel.StringInterface) mmmodel.StringInterface {
	if props == nil {
		return make(mmmodel.StringInterface)
	}
	return maps.Clone(props)
}

// validatePropsSize enforces the serialized-size cap on a Props map.
// The where argument identifies the calling operation for logs; the message keys are shared across callers.
func validatePropsSize(where, details string, props mmmodel.StringInterface, maxBytes int) *mmmodel.AppError {
	if props == nil {
		return nil
	}
	b, err := json.Marshal(props)
	if err != nil {
		return mmmodel.NewAppError(where, "model.shared.props_invalid.app_error", nil, details, http.StatusBadRequest)
	}
	if len(b) > maxBytes {
		return mmmodel.NewAppError(where, "model.shared.props_too_large.app_error", map[string]any{"MaxBytes": maxBytes}, details, http.StatusBadRequest)
	}
	return nil
}

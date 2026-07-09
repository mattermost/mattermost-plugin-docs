// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package model_test

import (
	"strings"
	"testing"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// validatePropsSize is unexported; exercise it through Page.IsValid which delegates to it.
func TestValidatePropsSize(t *testing.T) {
	t.Run("nil props passes", func(t *testing.T) {
		p := validPage()
		p.Props = nil
		require.Nil(t, p.IsValid())
	})

	t.Run("empty props passes", func(t *testing.T) {
		p := validPage()
		p.Props = mmmodel.StringInterface{}
		require.Nil(t, p.IsValid())
	})

	t.Run("props within limit passes", func(t *testing.T) {
		p := validPage()
		p.Props = mmmodel.StringInterface{"key": "value"}
		require.Nil(t, p.IsValid())
	})

	t.Run("props exceeding limit fails", func(t *testing.T) {
		p := validPage()
		// Build a value larger than PagePropsMaxBytes.
		p.Props = mmmodel.StringInterface{"key": strings.Repeat("x", model.PagePropsMaxBytes+1)}
		err := p.IsValid()
		require.NotNil(t, err)
		require.Equal(t, "model.shared.props_too_large.app_error", err.Id)
	})
}

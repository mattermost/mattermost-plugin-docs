package importer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-docs/server/model"
)

// TestContractLimitsMatchModel pins the mirrored contract limits to the real
// destination limits.
//
// The importer package is a byte-for-byte mirror of the mmetl producer and
// therefore restates these numbers as literals rather than importing them. That
// keeps the two implementations comparable, but it means a change to a Docs page
// limit would otherwise silently desynchronize the two repositories: the
// exporter would keep emitting pages this package accepts and the page store
// then rejects. This test turns that drift into a build failure here, where the
// fix is to update both mirrors and the contract document together.
func TestContractLimitsMatchModel(t *testing.T) {
	require.Equal(t, model.PageTitleMaxRunes, TitleMaxRunes)
	require.Equal(t, model.PageBodyMaxBytes, BodyMaxBytes)
	require.Equal(t, model.PagePropsMaxBytes, PropsMaxBytes)
	require.Equal(t, model.MaxPageDepth, MaxPageDepth)

	require.Less(t, PropsTargetBytes, PropsMaxBytes,
		"the exporter target must leave the importer room for its own metadata namespace")
}

package main

import (
	"fmt"
	"reflect"

	mmmodel "github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"

	docsmodel "github.com/mattermost/mattermost-plugin-docs/server/model"
)

const (
	newSpaceDefaultPresetContribute = "contribute"
	newSpaceDefaultPresetComment    = "comment"
	newSpaceDefaultPresetReadOnly   = "read_only"
)

// configuration captures the plugin's external configuration as exposed in the Mattermost server
// configuration, as well as values computed from the configuration. Any public fields will be
// deserialized from the Mattermost server configuration in OnConfigurationChange.
//
// As plugins are inherently concurrent (hooks being called asynchronously), and the plugin
// configuration can change at any time, access to the configuration must be synchronized. The
// strategy used in this plugin is to guard a pointer to the configuration, and clone the entire
// struct whenever it changes. You may replace this with whatever strategy you choose.
//
// If you add non-reference types to your configuration struct, be sure to rewrite Clone as a deep
// copy appropriate for your types.
type configuration struct {
	NewSpaceDefaultPreset string `json:"newSpaceDefaultPreset"`
}

func defaultConfiguration() *configuration {
	return &configuration{NewSpaceDefaultPreset: newSpaceDefaultPresetContribute}
}

func (c *configuration) setDefaults() {
	if c.NewSpaceDefaultPreset == "" {
		c.NewSpaceDefaultPreset = newSpaceDefaultPresetContribute
	}
}

func (c *configuration) validate() error {
	switch c.NewSpaceDefaultPreset {
	case newSpaceDefaultPresetContribute, newSpaceDefaultPresetComment, newSpaceDefaultPresetReadOnly:
		return nil
	default:
		return fmt.Errorf("newSpaceDefaultPreset must be one of %q, %q, or %q", newSpaceDefaultPresetContribute, newSpaceDefaultPresetComment, newSpaceDefaultPresetReadOnly)
	}
}

// newSpaceDefaultPermissions resolves the configured logical preset to the wire permission set
// consumed by CreateSpace. Configuration deliberately stores no database scheme id: core owns
// those ids, while CreateSpace's Service.resolveSpaceScheme (server/app/space.go) maps this
// permission set to the appropriate seeded scheme on every creation.
func (c *configuration) newSpaceDefaultPermissions() []string {
	schemeName := mmmodel.SchemeNameSpaceContribute
	switch c.NewSpaceDefaultPreset {
	case newSpaceDefaultPresetComment:
		schemeName = mmmodel.SchemeNameSpaceComment
	case newSpaceDefaultPresetReadOnly:
		schemeName = mmmodel.SchemeNameSpaceReadOnly
	}
	permissions, _ := docsmodel.DefaultPermissionsForSchemeName(schemeName)
	return permissions
}

// Clone shallow copies the configuration. Your implementation may require a deep copy if
// your configuration has reference types.
func (c *configuration) Clone() *configuration {
	clone := *c
	return &clone
}

// getConfiguration retrieves the active configuration under lock, making it safe to use
// concurrently. The active configuration may change underneath the client of this method, but
// the struct returned by this API call is considered immutable.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return defaultConfiguration()
	}

	return p.configuration
}

// setConfiguration replaces the active configuration under lock.
//
// Do not call setConfiguration while holding the configurationLock, as sync.Mutex is not
// reentrant. In particular, avoid using the plugin API entirely, as this may in turn trigger a
// hook back into the plugin. If that hook attempts to acquire this lock, a deadlock may occur.
//
// This method panics if setConfiguration is called with the existing configuration. This almost
// certainly means that the configuration was modified without being cloned and may result in
// an unsafe access.
func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	if configuration != nil && p.configuration == configuration {
		// Ignore assignment if the configuration struct is empty. Go will optimize the
		// allocation for same to point at the same memory address, breaking the check
		// above.
		if reflect.ValueOf(*configuration).NumField() == 0 {
			return
		}

		panic("setConfiguration called with the existing configuration")
	}

	p.configuration = configuration
}

// snapshotFeatureFlags caches the feature flags the request path reads, so per-request checks
// don't fetch the full server configuration across the plugin RPC boundary.
func (p *Plugin) snapshotFeatureFlags(cfg *mmmodel.Config) {
	p.enableDocs.Store(cfg != nil && cfg.FeatureFlags != nil && cfg.FeatureFlags.EnableDocs)
}

// OnConfigurationChange is invoked when configuration changes may have been made.
func (p *Plugin) OnConfigurationChange() error {
	p.snapshotFeatureFlags(p.API.GetConfig())

	configuration := new(configuration)

	// Load the public configuration fields from the Mattermost server configuration.
	if err := p.API.LoadPluginConfiguration(configuration); err != nil {
		return errors.Wrap(err, "failed to load plugin configuration")
	}
	configuration.setDefaults()
	if err := configuration.validate(); err != nil {
		return errors.Wrap(err, "invalid plugin configuration")
	}

	p.setConfiguration(configuration)

	return nil
}

func (p *Plugin) newSpaceDefaultPermissions() []string {
	return p.getConfiguration().newSpaceDefaultPermissions()
}

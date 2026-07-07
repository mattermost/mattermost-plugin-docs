// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"path/filepath"
	"sync"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/mattermost/mattermost/server/public/shared/i18n"
	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-docs/server/app"
	"github.com/mattermost/mattermost-plugin-docs/server/store"
)

// Plugin implements the Mattermost plugin interface.
type Plugin struct {
	plugin.MattermostPlugin

	store   *store.Store
	service *app.Service
	client  *pluginapi.Client
	router  *mux.Router

	// configurationLock synchronizes access to configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration. Consult getConfiguration and
	// setConfiguration for usage.
	configuration *configuration
}

// OnActivate initializes the store, runs migrations, and wires up the service and router.
func (p *Plugin) OnActivate() error {
	bundlePath, err := p.API.GetBundlePath()
	if err != nil {
		return errors.Wrap(err, "failed to get bundle path")
	}
	if translErr := i18n.TranslationsPreInit(filepath.Join(bundlePath, "assets", "i18n")); translErr != nil {
		return errors.Wrap(translErr, "failed to load translation files")
	}

	p.client = pluginapi.NewClient(p.API, p.Driver)

	masterDB, err := p.client.Store.GetMasterDB()
	if err != nil {
		return errors.Wrap(err, "failed to get master DB")
	}
	s, err := store.New(masterDB, p.client.Store.DriverName())
	if err != nil {
		return errors.Wrap(err, "failed to create store")
	}
	s.SetLogger(&p.client.Log)

	if migErr := s.RunMigrations(); migErr != nil {
		return errors.Wrap(migErr, "failed to run docs migrations")
	}
	p.store = s
	p.service = app.New(p.store, &p.client.Log)

	p.router = p.initRouter()

	return nil
}

// OnDeactivate is a no-op. It does not close the store's DB handle: that handle is the server's
// shared master DB obtained via GetMasterDB, not one this plugin owns. It also does not clear
// store/service/router: those fields are read without synchronization by ServeHTTP and handlers,
// so niling them here would race an in-flight request against deactivation.
func (p *Plugin) OnDeactivate() error {
	return nil
}

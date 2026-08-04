// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/gorilla/mux"
	mmmodel "github.com/mattermost/mattermost/server/public/model"
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

	// enableDocs mirrors FeatureFlags.EnableDocs, refreshed by snapshotFeatureFlags on every
	// configuration change. Per-request reads use it instead of fetching the full config.
	enableDocs atomic.Bool

	// Import upload admission. Only one bundle inspection runs per process, which bounds temporary
	// disk use as well as parser and database work.
	//
	// inspectionMu guards inspectionClosed and serializes it with inspectionWG.Add: checking "closed"
	// and registering the in-flight inspection must be one atomic step, or a deactivation could slip
	// between them and close the store while a new inspection was starting.
	inspectionMu        sync.Mutex
	inspectionClosed    bool
	inspectionWG        sync.WaitGroup
	inspectionSemaphore chan struct{}
}

// importInspectionSlots is the number of concurrent bundle inspections allowed per process.
const importInspectionSlots = 1

// initImportAdmission opens the import inspection gate. It must run before the router serves any
// request: with a nil semaphore every upload would be rejected as busy.
func (p *Plugin) initImportAdmission() {
	p.inspectionMu.Lock()
	defer p.inspectionMu.Unlock()
	p.inspectionClosed = false
	p.inspectionSemaphore = make(chan struct{}, importInspectionSlots)
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
	// Wire the loaded bundle into AppError translation: without this, every NewAppError freezes
	// its Message to the raw message id, and parameterized messages ({{.MaxDepth}} etc.) can never
	// be rendered — the params map is not serialized to clients. The admin-configured server
	// locale never reaches a plugin process through i18n's own system-locale path, so it is
	// resolved from config here; GetUserTranslations falls back to English when the locale or
	// its bundle is absent.
	cfg := p.API.GetConfig()
	locale := ""
	if cfg != nil {
		locale = mmmodel.SafeDereference(cfg.LocalizationSettings.DefaultServerLocale)
	}
	mmmodel.AppErrorInit(i18n.GetUserTranslations(locale))

	// Seed the EnableDocs cache so the API gate works from the first request; relying on
	// OnConfigurationChange alone would leave the zero-value (501 on every route) on servers
	// where Docs is already enabled at activation.
	p.snapshotFeatureFlags(cfg)
	if !p.enableDocs.Load() {
		p.API.LogWarn("EnableDocs feature flag is not enabled; every Docs API route returns 501 until a server built with Docs core support enables it")
	}

	p.client = pluginapi.NewClient(p.API, p.Driver)

	masterDB, err := p.client.Store.GetMasterDB()
	if err != nil {
		return errors.Wrap(err, "failed to get master DB")
	}
	s, err := store.New(masterDB, p.client.Store.DriverName(), &p.client.Log)
	if err != nil {
		return errors.Wrap(err, "failed to create store")
	}

	if migErr := s.RunMigrations(); migErr != nil {
		if closeErr := s.Close(); closeErr != nil {
			p.API.LogError("Failed to close store after failed activation", "err", closeErr)
		}
		return errors.Wrap(migErr, "failed to run docs migrations")
	}
	p.store = s
	p.service = app.New(p.store, &p.client.Log, p.client)

	// The inspection gate must exist before the router accepts requests.
	p.initImportAdmission()

	p.router = p.initRouter()

	return nil
}

// OnDeactivate closes the plugin-owned DB connection pool opened by GetMasterDB. It does not nil
// store/service/router: those fields are read without synchronization by ServeHTTP and handlers,
// so niling them here would race an in-flight request against deactivation.
func (p *Plugin) OnDeactivate() error {
	// Close the import inspection gate first, then wait for every already-admitted inspection and its
	// staging transaction to finish. Closing the store while one is still running would fail an
	// upload that had already been accepted.
	p.inspectionMu.Lock()
	p.inspectionClosed = true
	p.inspectionMu.Unlock()
	p.inspectionWG.Wait()

	if p.store != nil {
		if err := p.store.Close(); err != nil {
			p.API.LogError("Failed to close store", "err", err)
		}
	}
	return nil
}

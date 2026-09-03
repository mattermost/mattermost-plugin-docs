// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

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

	// The worker goroutine is the single V1 importer: it processes job work and runs the hourly
	// maintenance sweep (expiry, staged-body purge, retention deletion) from the same loop, so cleanup can
	// never overlap the work whose capacity it is reclaiming.
	workerCancel context.CancelFunc
	workerWG     sync.WaitGroup
}

// importInspectionSlots is the number of concurrent bundle inspections allowed per process.
const importInspectionSlots = 1

// importMaintenanceInterval is how often the maintenance sweep runs.
const importMaintenanceInterval = time.Hour

// importWorkerIdleInterval is how long the worker waits before re-checking for work when it found none.
// Job creation does not signal the worker, so this is the latency between an upload being accepted and
// its preflight starting; short enough to feel immediate, long enough to be a negligible query load.
const importWorkerIdleInterval = 2 * time.Second

// importWorkerErrorBackoff is how long the worker waits after a failed pass. It is deliberately much longer
// than the idle interval: an idle worker is healthy and should stay responsive, while a failing one is usually
// failing for a reason that will not have changed two seconds later.
const importWorkerErrorBackoff = 30 * time.Second

// importWorkerDelay decides what the worker does after one pass: drain straight into the next unit of work, or
// wait, and for how long.
//
// A failed pass always waits, whatever it reported doing. RunImportWork reports worked=true whenever it selected
// a job, error or not, so draining on error would spin the same failing job as fast as the database can answer —
// turning one persistent fault, a backend outage or a job that cannot advance, into a CPU and log storm. The
// error wait is much longer than the idle wait because an idle worker is healthy and should stay responsive,
// while a failing one is usually failing for a reason that will not have changed two seconds later.
func importWorkerDelay(worked bool, err error) (time.Duration, bool) {
	switch {
	case err != nil:
		return importWorkerErrorBackoff, false
	case worked:
		return 0, true
	default:
		return importWorkerIdleInterval, false
	}
}

// startImportWorker launches the single import worker.
//
// It drains available work before idling, so a backlog is worked down promptly rather than one job per
// tick. One maintenance pass runs immediately at startup: a restart after downtime must reclaim capacity
// from work abandoned while the server was down rather than waiting a full hour, and startup is also when
// interrupted jobs are recovered.
func (p *Plugin) startImportWorker() {
	ctx, cancel := context.WithCancel(context.Background())
	p.workerCancel = cancel
	p.workerWG.Go(func() {
		p.service.LogImportWorkerInvariants()
		p.service.LogImportMaintenance(p.service.RunImportMaintenance())

		maintenance := time.NewTicker(importMaintenanceInterval)
		defer maintenance.Stop()
		idle := time.NewTimer(importWorkerIdleInterval)
		defer idle.Stop()

		for {
			// Cancellation is checked between units of work rather than mid-transaction, so deactivation
			// never abandons a half-applied job.
			select {
			case <-ctx.Done():
				return
			default:
			}

			worked, err := p.service.RunImportWork()
			if err != nil {
				p.API.LogError("Import worker pass failed; backing off before retrying", "err", err)
			}
			delay, drain := importWorkerDelay(worked, err)
			if drain {
				continue
			}
			idle.Reset(delay)
			select {
			case <-ctx.Done():
				return
			case <-maintenance.C:
				p.service.LogImportMaintenance(p.service.RunImportMaintenance())
			case <-idle.C:
			}
		}
	})
}

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
	p.startImportWorker()

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

	// Stop the worker and wait for its in-flight unit of work: it runs database transactions, so the
	// store must outlive it.
	if p.workerCancel != nil {
		p.workerCancel()
	}
	p.workerWG.Wait()

	if p.store != nil {
		if err := p.store.Close(); err != nil {
			p.API.LogError("Failed to close store", "err", err)
		}
	}
	return nil
}

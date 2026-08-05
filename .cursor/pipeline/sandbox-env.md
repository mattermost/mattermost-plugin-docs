# Sandbox environment state (Fase 2)

| Componente | Estado | Detalle |
|---|---|---|
| Postgres | UP | v16.14, cluster local `main`, puerto 5432, `postgres/postgres`, DB `mattermost_test` |
| Mattermost server | UP | Compilado desde `mattermost/mattermost` @ `master` (commit `61bc7f18`, 2026-08-04), binario en `.cursor/pipeline/reference-repos/mm-server`, v11.11.0 |
| Fuente mattermost/mattermost | Clon shallow (depth 1) en `.cursor/pipeline/reference-repos/mattermost` (`git init` + `git fetch --depth 1 origin master` + `reset --hard FETCH_HEAD`) |
| go.work | Creado en `.cursor/pipeline/reference-repos/mattermost/go.work` (`use ./server ./server/public`) — necesario porque `server/go.mod` fija `server/public v0.4.0` (release antiguo) y sin `go.work` el build falla con símbolos no definidos (`ChannelJoinRequest`, etc. de otras features en desarrollo). Confirmado: `ChannelTypeSpace` y `EnableDocs` SÍ están mergeados en master público (commit `5f7f967a7dbfbfe3fe7dae96ad6b7142268be5bc`, PR #37321 "Add ChannelTypeSpace backing-channel type for Docs"). |
| Proceso servidor | tmux session `mm-server`, comando `./mm-server --config .../mm-data/config/config.json server`, escuchando en `:8065` |
| Config server | Generado por defecto al arrancar (no había template en el repo `mattermost/mattermost` bajo `server/config/`); overrides vía env vars `MM_*` (ver abajo) |
| EnableDocs flag | `true` vía `MM_FEATUREFLAGS_ENABLEDOCS=true`. Confirmado activo: `GET /api/v4/config/client` → `FeatureFlagEnableDocs: "true"` |
| Plugin Docs | Desplegado vía `make dist` + `make deploy` (pluginctl local-mode socket) contra el `master` del propio repo `mattermost-plugin-docs` (rama `cursor/fase1-security-audit-findings-e2d4`, sin diff de código vs `origin/master`, solo ficheros `.cursor/pipeline/*.md` añadidos) |
| Migraciones Docs | Aplicadas limpiamente (`create_spaces`, `create_pages`, `create_drafts`, `add_page_originalid_index`, `add_draft_lastactiveat_baseeditat`) — confirmado en logs y `\dt docs_*` en psql |
| Team de prueba | `testteam` (id `i3ywcy9k43dn9x19a6agiwcxfy`) |
| Usuarios de prueba | `sysadmin` (system admin, primer usuario), `alice`, `bob`, `carol` (usuarios normales, miembros de `testteam`) — passwords en este documento son solo para este sandbox efímero |

## Variables de entorno del servidor Mattermost

```
MM_SQLSETTINGS_DRIVERNAME=postgres
MM_SQLSETTINGS_DATASOURCE=postgres://postgres:postgres@127.0.0.1:5432/mattermost_test?sslmode=disable&connect_timeout=10
MM_SERVICESETTINGS_SITEURL=http://localhost:8065
MM_SERVICESETTINGS_LISTENADDRESS=:8065
MM_SERVICESETTINGS_ENABLELOCALMODE=true
MM_PLUGINSETTINGS_ENABLE=true
MM_PLUGINSETTINGS_ENABLEUPLOADS=true
MM_PLUGINSETTINGS_DIRECTORY=/workspace/.cursor/pipeline/mm-data/plugins
MM_PLUGINSETTINGS_CLIENTDIRECTORY=/workspace/.cursor/pipeline/mm-data/client-plugins
MM_FEATUREFLAGS_ENABLEDOCS=true
MM_LOGSETTINGS_ENABLECONSOLE=true
MM_FILESETTINGS_DIRECTORY=/workspace/.cursor/pipeline/mm-data/data
MM_TEAMSETTINGS_ENABLEOPENSERVER=true
MM_RATELIMITSETTINGS_ENABLE=false
MM_LOGSETTINGS_ENABLEFILE=true
MM_LOGSETTINGS_FILELOCATION=/workspace/.cursor/pipeline/mm-data/logs
```

## Credenciales de prueba (solo sandbox efímero)

| Usuario | Password | Rol |
|---|---|---|
| admin@example.com (`sysadmin`) | Sysadmin123! | system_admin, primer usuario |
| alice@example.com (`alice`) | Testuser123! | miembro normal de testteam |
| bob@example.com (`bob`) | Testuser123! | miembro normal de testteam |
| carol@example.com (`carol`) | Testuser123! | miembro normal de testteam |

Tokens de sesión guardados en `/tmp/token_{alice,bob,carol}.txt` y `/tmp/admin_token.txt` (no persisten fuera de esta sesión del sandbox).

## Cómo relanzar (si el proceso muere)

```bash
sudo pg_ctlcluster 16 main start   # si postgres no está up
tmux -f /exec-daemon/tmux.portal.conf attach -t mm-server   # o send-keys para reiniciar
cd /workspace/.cursor/pipeline/reference-repos
# (exportar las MM_* de arriba)
./mm-server --config /workspace/.cursor/pipeline/mm-data/config/config.json server
```

Para redesplegar el plugin tras cambios de código: `cd /workspace && MM_SERVICESETTINGS_SITEURL=http://localhost:8065 MM_ADMIN_USERNAME=sysadmin MM_ADMIN_PASSWORD='Sysadmin123!' make deploy`.

## Limitaciones conocidas del entorno

- SMTP no configurado (localhost:10025 no existe) → warnings de email de bienvenida, sin impacto en los tests planeados.
- Sin licencia Enterprise cargada (Team Edition) — no afecta a Docs, que no requiere licencia.
- `client dir` warning al arrancar (`failed to find client dir`) porque no se compiló el webapp completo de `mattermost/mattermost` (solo el binario de servidor) — irrelevante para pruebas vía API REST directa.

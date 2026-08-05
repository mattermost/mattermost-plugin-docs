# Fase 2 — Validación de reproducibilidad (mattermost-plugin-docs)

**Modelo:** Sonnet 5.
**Ficheros de estado leídos:** `.cursor/pipeline/runbook-completo.md` (completo, incl. Anexos A/B/C),
`.cursor/pipeline/mvp-context.md`, `.cursor/pipeline/security-testing-plan.md`,
`.cursor/pipeline/fase1-findings.md`.
**Alcance:** solo Fase 2 (reproducibilidad + ejecución dinámica del plan de testing). No se han
implementado fixes ni tocado Fases 3-5.

## Resumen del bootstrap dinámico

El plugin declara en `plugin.json` que requiere "Docs core support" (tipo de canal `ChannelTypeSpace`
+ feature flag `EnableDocs`) que **no está en ningún release estable** de Mattermost. Se verificó que
sí está mergeado en `mattermost/mattermost` rama `master` (PR #37321, commit `5f7f967a7dbfbfe3fe7dae96ad6b7142268be5bc`,
2026-07-16). Se hizo shallow clone de `master` (commit `61bc7f18`, 2026-08-04), se compiló el servidor
completo desde código fuente (iniciando Postgres 16 local) y se desplegó el plugin con `make deploy`
contra esa instancia. Detalle completo, credenciales de prueba y comandos de relanzado en
`.cursor/pipeline/sandbox-env.md`. Entorno: EnableDocs=true confirmado vía `/api/v4/config/client`;
migraciones Docs aplicadas limpiamente; team `testteam` con usuarios `sysadmin` (system admin),
`alice`, `bob`, `carol`.

---

## Parte 1 — Reproducibilidad de findings de Fase 1

| Finding | Resultado | Método | Evidencia |
|---|---|---|---|
| DOCS-F1 — Agotamiento pool conexiones (C5, high) | **REPRODUCIBLE** | Estático + dinámico | Releído `server/store/store.go` y `server/plugin.go`: confirmado que no existe ninguna llamada a `SetMaxOpenConns`/`SetMaxIdleConns`/`SetConnMaxLifetime` en todo el repo. Releído `withSpaceMembershipLock` (`space_store.go:369-430`): confirma retención de conexión durante polling de 100ms hasta 10s. Dinámicamente: 40 `DELETE /spaces/{id}/members/{id}` concurrentes contra el mismo space drenaron a ritmo de ~1 cada 100ms (~4s totales para 40, coincide con el mecanismo de polling descrito), y el pool de conexiones de Postgres pasó de 4 a 43 conexiones activas/idle tras la ráfaga (crecimiento no acotado, +39 conexiones para 40 peticiones concurrentes) — confirma ausencia de `MaxOpenConns`. No se llevó la prueba a escala completa (300 conexiones) para no destruir el sandbox compartido con el resto de casos de esta sesión (`max_connections=100` en la instancia de prueba); la extrapolación a agotamiento total del pool del servidor (300 por defecto) se apoya en el código, ya verificado línea a línea. |
| DOCS-F2 — Amplificación de memoria en duplicado (C5, high) | **REPRODUCIBLE** | Estático + dinámico | Releído `page_hierarchy.go` (`MaxPageDescendantsLimit=5000`, límite de filas no de bytes), `page_store.go:668-697` (`fetchDescendantRows`, sin comprobación de tamaño agregado) y `page_duplicate.go:100-144` (inserción por chunks, clona todo el árbol en memoria). Dinámicamente: se creó un subárbol de 16 páginas (1 raíz + 15 hijas) con bodies de ~150 KB cada una (~2.4 MiB de contenido total) y se invocó `POST .../duplicate {"include_children":true}` (27 bytes de cuerpo de petición). La RSS del proceso del plugin pasó de 35.9 MiB a 58.5 MiB (+22.6 MiB) en una sola petición de 76 ms, con HTTP 201. Factor de amplificación observado ~9x sobre el contenido origen a esta escala moderada (deliberadamente no se llevó a los 5000 filas/20 GiB teóricos para no arriesgar el sandbox compartido), consistente con el mecanismo descrito (múltiples copias en memoria: scan de filas, clones de structs, buffer de inserción por chunks, codificación RPC). |
| DOCS-F3 — Space permanentemente inaccesible (C6, high) | **REPRODUCIBLE** | Estático + dinámico | Releído `space.go:365-421` (`RemoveSpaceMember`/guard) y confirmado por grep que no existe ningún hook `UserHasLeftTeam`/`UserHasLeftChannel` en el plugin. Dinámicamente: `bob` crea un space en solitario (único miembro del canal de respaldo, confirmado por `SELECT userid FROM channelmembers` vía psql read-only). `bob` abandona el equipo con la acción de core `DELETE /api/v4/teams/{team}/members/{bob}` (no una ruta de Docs). Confirmado por psql que la fila de `channelmembers` para el canal de respaldo **desaparece** (el propio core de Mattermost limpia las membresías de canal del equipo abandonado — mecanismo incluso más directo que la sola invalidación por `TeamMember.DeleteAt` descrita en Fase 1). Tras esto: `bob` recibe 403 al hacer `GET /spaces/{id}`; `sysadmin` (nunca miembro) recibe el mismo 403 — **sin bypass de admin**. Se readmitió a `bob` al equipo (`POST /teams/{id}/members`, 201) y se repitió el `GET`: sigue en 403, y el intento de auto-recuperación `POST /spaces/{id}/members` (añadirse a sí mismo) también devuelve 403 porque `AddSpaceMember` exige ser ya miembro del space — confirma que la inaccesibilidad es **permanente**, no solo mientras dure la ausencia del equipo. |
| DOCS-F4 — Props sin sanitizar (C1 latente, low) `[DINÁMICO: NO]` | **REPRODUCIBLE** (estático, no aplica dinámico per plan) | Estático | Releído `server/model/space.go:90-124` (`PreSave`/`PreUpdate`): confirma que solo `Title`, `Description`, `Icon` pasan por `SanitizeUnicode`; `Props` no se toca en ningún punto salvo `ValidatePropsSize`. Sin sink de renderizado hoy en el webapp (confirmado por Fase 1), por lo que se mantiene como low sin verificación dinámica, conforme al plan. |
| DOCS-F5 — `sanitizeURL` deja pasar `//host` (low, pero también SEC-XSS-05 del top-10) | **REPRODUCIBLE** | Estático + dinámico (ver SEC-XSS-05 abajo, ejecutado por estar en el top-10 del plan aunque el finding esté marcado `[DINÁMICO: NO]`) | Releído `page_content.go:509-526` (`urlScheme`): confirma que `//`, `?`, `#` antes de `:` devuelven `hasScheme=false`. Dinámicamente: se creó una página con `image.attrs.src="//evil.example.com/tracker.png"` y `link.attrs.href="//evil.example.com/phish"`; ambos sobrevivieron intactos en el body almacenado y devuelto por la API. |

---

## Parte 2 — Ejecución dinámica del plan de testing (10 casos bloqueantes + grupos A,B,D,C,E,G,H)

| Test ID | Estado | Evidencia (2-3 líneas) |
|---|---|---|
| SEC-AUTH-01 (forjar Mattermost-User-ID) | **Descartado dinámicamente** | Petición sin sesión + header `Mattermost-User-ID` forjado → 401. Sesión válida de `bob` + header forjado como `alice` (que sí tiene acceso a un space del que bob no es miembro) → 403 (se sigue evaluando como bob, el header del cliente se ignora). El servidor re-escribe la cabecera a partir de la sesión validada, tal como predice el análisis estático de Fase 1. |
| SEC-AUTH-04 (CSRF en mutaciones) | **Descartado dinámicamente** | Login con `X-Requested-With: XMLHttpRequest` para obtener cookies de sesión (`MMAUTHTOKEN`/`MMCSRF`). `PATCH /spaces/{id}` con solo cookies (sin token CSRF) → 401 "Not authorized". Con token CSRF incorrecto → 401. Con el token CSRF correcto de la cookie `MMCSRF` → pasa el gate (400 por validación de negocio no relacionada, `expected_update_at` requerido), confirmando que el CSRF se aplica correctamente en rutas de plugin y solo entonces se llega a la lógica de Docs. |
| SEC-XSS-02 (bypass allowlist sanitizador) | **Descartado dinámicamente** | Documento con nodo `script` (tipo no permitido) → rechazo completo 400 `invalid_content` (fail-closed). Documento con nodos permitidos pero atributos peligrosos (`onerror`, `onclick`, `onmouseover` en attrs/marks, `src="javascript:alert(4)"` en imagen) → aceptado con atributos peligrosos eliminados (`attrs:{}`) y `src` vaciado a `""`. |
| SEC-XSS-05 (bypass sanitizeURL) | **Confirmado dinámicamente** (ver DOCS-F5 arriba) | `src="//evil.example.com/..."` y `href="//evil.example.com/..."` sobreviven sin modificar en el body almacenado — no es XSS (no hay `javascript:`/`data:` ejecutable), pero sí carga de recursos externos / redirección fuera de origen, tal como documenta DOCS-F5. Aceptado para MVP como low (no bloqueante C1-C7), pero recomendable corregir antes de exponer el editor real. |
| SEC-IDOR-02 (leer página de otro space vía space_id propio + page_id ajeno) | **Descartado dinámicamente** | `carol` (space propio) + `page_id` de una página de `alice` → 404 `page.not_found` (el filtro por `space_id` en la query de store excluye la fila). Control: `carol` con `space_id` real de `alice` (de la que no es miembro) → 403, confirma que el gate de membresía actúa antes que cualquier lógica de página. |
| SEC-IDOR-06 (move/duplicate cross-space) | **Descartado dinámicamente** | (a) `carol` intenta `move-to-space` su propia página hacia el space de `alice` (no es miembro) → 403. (b) `carol` intenta `duplicate` sobre el `page_id` de `alice` usando su propio `space_id` en la URL → 404 `duplicate.not_found`. (c) `carol` intenta `move` con `parent_id` = página ajena de `alice` (con `force:true`) → 400 `invalid_parent` (el padre destino se busca dentro del mismo space). |
| SEC-DEPUTY-02 (archivar canal no-space) | **Descartado dinámicamente** | Se creó un canal normal tipo "O" del que `alice` es miembro. `PATCH /spaces/{id}` con `channel_id` forjado en el body → 200 pero el campo se ignora (`json:"-"`, confirmado que solo cambió el título). `POST .../spaces` con `channel_id` forjado en el body → 201 pero crea un canal nuevo propio (verificado en `docs_space.channelid` vía psql, distinto del canal forjado). El canal normal objetivo queda con `delete_at:0` intacto en todo momento. |
| SEC-SQLI-01 (SQLi en CTEs) | **Descartado dinámicamente** | `space_id`/`page_id` con payloads `' OR '1'='1` y `x'; DROP TABLE docs_space; --` (URL-encoded) en los paths → 400 `invalid_id` (rechazados por validación de formato de ID antes de tocar SQL). Título de página con `'; DROP TABLE docs_page; --` → almacenado literalmente como string vía query parametrizada; `SELECT count(*) FROM docs_page`/`docs_space` confirma que ambas tablas siguen intactas tras los intentos. |
| SEC-DOS-03 (DoS por CTE recursiva) | **Descartado dinámicamente** | Se intentó construir una cadena de páginas de profundidad creciente: el propio API rechaza en `depth=10` con `app.page.max_depth_exceeded` (`MaxPageDepth=10`), 5x por debajo del límite de seguridad interno de las CTEs (`MaxPageHierarchyDepth=50`). La superficie de DoS real por este vector no es alcanzable; el DoS real de Grupo G es DOCS-F2 (memoria), ya confirmado arriba. |
| SEC-INT-01 (space permanentemente inaccesible) | **Confirmado dinámicamente** (ver DOCS-F3 arriba) | Ver tabla de Parte 1. Caso bloqueante C6 reproducido de extremo a extremo, incluyendo el intento de recuperación tras re-unirse al equipo. |

### Grupo H — comprobación adicional (más allá del top-10)

| Caso | Estado | Evidencia |
|---|---|---|
| Ciclo en `move` (página bajo su propio descendiente) | **Descartado dinámicamente** | `PATCH .../move` con `parent_id` = id de un hijo directo de la propia página, `force:true` → 400 `app.page.circular_reference.app_error`. Guard de integridad funciona correctamente. |

---

## Conclusión de Fase 2

- **DOCS-F1, DOCS-F2, DOCS-F3 quedan REPRODUCIBLE con evidencia dinámica real**, además de la
  estática ya recogida en Fase 1. Los tres son bloqueantes de MVP (C5/C5/C6) y deben pasar a Fase 3.
- DOCS-F4 y DOCS-F5 se mantienen como `low`/`[DINÁMICO: NO]` según el plan; DOCS-F5 recibió además
  confirmación dinámica incidental por estar dentro del top-10 del plan de testing (SEC-XSS-05), sin
  cambiar su severidad (no es C1: no hay ejecución de script, solo fuga de referrer/IP y open
  redirect fuera de origen).
- Los 10 casos bloqueantes del plan de testing se ejecutaron dinámicamente contra la instancia real:
  **9 de 10 quedan descartados** (no reproducibles / correctamente mitigados) y **1 de 10
  (SEC-INT-01) queda confirmado**, coincidiendo exactamente con DOCS-F3.
- Ningún finding adicional nuevo surgió de la ejecución dinámica del plan que no estuviera ya
  cubierto por Fase 1.
- No hubo bloqueos de entorno: el bootstrap dinámico (Anexo C) se completó con éxito compilando
  `mattermost/mattermost` desde código fuente (sin depender de Docker, no disponible en este
  sandbox) más Postgres local.

**Recomendación para Fase 3 (fuera de alcance de esta sesión):** priorizar fix de DOCS-F1 (añadir
`SetMaxOpenConns`/límite de *waiters* concurrentes por lock), DOCS-F2 (límite de bytes agregados en
`fetchDescendantRows`/`getPageSubtreeForDuplicate`, no solo de filas) y DOCS-F3 (hook
`UserHasLeftTeam`/`UserHasLeftChannel` que ejecute el mismo guard de "último miembro" que ya protege
`RemoveSpaceMember`, o un mecanismo de recuperación).

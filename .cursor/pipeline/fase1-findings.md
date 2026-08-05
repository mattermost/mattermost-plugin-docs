# Fase 1 — Auditoría de seguridad exhaustiva (mattermost-plugin-docs)

**Modelo:** Opus 5 (Claude Opus 4.x, identificador de sesión `opus-5`)
**Alcance:** auditoría de arquitectura sobre el estado completo del repo `/workspace` (no un diff).
**Ficheros de estado leídos:** `.cursor/pipeline/runbook-completo.md`, `.cursor/pipeline/mvp-context.md`,
`.cursor/pipeline/security-testing-plan.md`.
**Cobertura Pasada A:** todo `server/` no-test (`api.go`, `api_space.go`, `api_page.go`,
`api_page_drafts.go`, `api_page_presence.go`, `plugin.go`, `configuration.go`, `app/*.go`,
`model/*.go`, `store/*.go`) + pasada ligera sobre `webapp/src/**`. Se verificó además el contrato
real del `pluginapi` vendorizado (`pluginapi/store.go`, `shared/driver/*.go`, `model/channel.go`)
para no asumir semántica de la plataforma.

---

## Resumen de findings confirmados (TRUE POSITIVE)

| ID | Título | Grupo | Criterio | Severidad | Fichero:línea principal |
|----|--------|-------|----------|-----------|--------------------------|
| DOCS-F1 | Agotamiento del pool de conexiones maestro de Mattermost desde rutas Docs autenticadas | G | C5 | **high** | `server/store/space_store.go:369-430`, `server/plugin.go:75-79` |
| DOCS-F2 | Amplificación de memoria sin límite en `duplicate?include_children=true` | G | C5 | **high** | `server/store/page_store.go:668-697`, `server/store/page_duplicate.go:123-138` |
| DOCS-F3 | Space permanentemente inaccesible: el guard de último miembro no cubre la salida de equipo | H | C6 | **high** | `server/app/space.go:365-421`, `server/plugin.go` (sin hooks) |
| DOCS-F4 | `Props` de Space y Page se almacenan y devuelven sin sanitizar (XSS almacenado latente) | D | C1 | **low** `[DINÁMICO: NO]` | `server/model/space.go:84-124`, `server/api_space.go:89-96` |
| DOCS-F5 | `sanitizeURL` deja pasar referencias sin esquema (`//evil.com`) en `href`/`src` | D | C1 | **low** `[DINÁMICO: NO]` | `server/model/page_content.go:567-583` |

Pasan a Fase 2: DOCS-F1, DOCS-F2, DOCS-F3, DOCS-F4, DOCS-F5.

---

## DOCS-F1 — Agotamiento del pool de conexiones maestro de Mattermost (C5, high)

**Grupo:** G (DoS) · **Caso relacionado:** SEC-DOS-03 (variante: no es la CTE, es el pool)

### Evidencia

1. `server/plugin.go:75-79` — el store se construye sobre `p.client.Store.GetMasterDB()`.
2. `server/store/store.go:74-85` — `store.New` envuelve ese `*sql.DB` en `sqlx` y **nunca llama a
   `SetMaxOpenConns` / `SetMaxIdleConns` / `SetConnMaxLifetime`**. Un `grep` sobre todo el repo no
   encuentra ninguna de esas llamadas.
3. `pluginapi/store.go` (`initializeMaster`) hace `sql.OpenDB(driver.NewConnector(driver, true))`.
   El default de `database/sql` para `MaxOpenConns` es **0 = ilimitado**.
4. `shared/driver/driver.go` documenta explícitamente el modelo: *"lets everyone use the central
   connection pool in the server"*. Cada `Connector.Connect` hace `c.api.Conn(isMaster)`, es decir
   **toma y retiene una conexión del pool maestro del propio servidor Mattermost** hasta que el
   plugin la cierra. `SqlSettings.MaxOpenConns` por defecto es 300 en el servidor.
5. Dos puntos del plugin retienen esa conexión durante mucho más que una consulta:
   - `server/store/space_store.go:369-430` (`withSpaceMembershipLock`): toma la conexión en
     `s.db.Connx(ctx)` (línea 372) y entra en un bucle de `pg_try_advisory_lock` con reintentos
     cada 100 ms (`spaceMembershipLockRetryInterval`, línea 352) hasta
     `spaceMembershipLockAcquireTimeout = 10 * time.Second` (línea 350). Un *waiter* que nunca
     obtiene el lock retiene la conexión los 10 s completos. El propio comentario de las líneas
     346-349 reconoce el riesgo ("*each holding a pooled connection*") pero solo lo acota en el
     tiempo, no en el número de *waiters* concurrentes.
   - `server/store/store.go:228-231` (`advisoryXactLock` → `pg_advisory_xact_lock`, bloqueante) usado
     por `lockSiblingGroup` (`server/store/page_move.go:684-692`) y `nextSortOrder`
     (`page_move.go:698-701`). Aquí no hay *try*: la espera se acota solo por
     `defaultQueryTimeout = 30 * time.Second` (`store.go:58`), y se mantiene dentro de una
     transacción abierta, es decir con conexión de pool + backend de Postgres retenidos.
6. `server/app/space.go:386` es la ruta REST que llega a (5a): `DELETE
   /api/v1/spaces/{space_id}/members/{user_id}` (`server/api_space.go:195-209`). El guard
   `hasOtherAuthorizedMember` corre **dentro** del lock e incluye RPC remotos
   (`Channel.ListMembers`, `Team.GetMember`), por lo que el titular del lock lo retiene durante
   varias llamadas de red.

### Cadena de ataque

1. Atacante = cualquier empleado autenticado, miembro de un space `S` cualquiera (basta con crear
   uno propio; `POST /teams/{team_id}/spaces` está permitido a todo miembro del equipo — exclusión
   MVP aceptada, aquí solo se usa como setup).
2. Lanza ~300 peticiones concurrentes de coste trivial (cuerpo vacío, ~200 bytes cada una):
   `DELETE /plugins/com.mattermost.docs/api/v1/spaces/{S}/members/{cualquier_id_valido}`.
   No hace falta que el target sea miembro: `RemoveSpaceMember` toma el lock **antes** de
   descubrirlo (`server/app/space.go:386-405`, y el comentario de 387-388 lo confirma).
3. La primera petición gana el advisory lock; las 299 restantes entran en el bucle de polling de
   `withSpaceMembershipLock`. El *hand-off* del lock está limitado por el intervalo de polling de
   100 ms, así que la cola drena a ~10 adquisiciones/s: con 300 *waiters*, la gran mayoría agota
   los 10 s completos.
4. Cada *waiter* mantiene una conexión del pool del plugin, y cada conexión del plugin mantiene
   una conexión del pool maestro del servidor Mattermost (paso 4 de Evidencia). Como el pool del
   plugin es ilimitado, no hay contrapresión: se abren tantas como peticiones concurrentes haya.
5. A ~300 concurrentes se agota `SqlSettings.MaxOpenConns` (default 300). A partir de ahí **toda**
   operación de base de datos del servidor —login, envío de posts, WebSocket, cualquier plugin—
   se bloquea esperando conexión. Denegación de servicio de la instancia completa, no solo de Docs.
6. Manteniendo el ritmo de peticiones el estado se sostiene indefinidamente. Variante equivalente y
   más barata en tiempo de retención (30 s en vez de 10 s): N peticiones concurrentes
   `PATCH /spaces/{S}/pages/{page_id}/move` con el mismo `parent_id`, que contienden en
   `pg_advisory_xact_lock` bloqueante dentro de transacción.

### Impacto

C5 — DoS del servidor Mattermost completo, desencadenable por un único usuario autenticado sin
privilegios especiales, con peticiones de unos cientos de bytes y sin necesidad de datos previos.
No hay rate limiting activo por defecto (`RateLimitSettings.Enable = false`).

### Refutaciones consideradas y descartadas

- *"El timeout de 10 s ya acota el problema"*: acota la duración de cada retención, no el número de
  retenciones simultáneas. El recurso que se agota es el conteo de conexiones, no el tiempo.
- *"Es un problema genérico de cualquier plugin que use `GetMasterDB`"*: parcialmente cierto, pero
  un plugin normal retiene la conexión durante milisegundos. Docs la retiene 10 s (polling) y 30 s
  (lock transaccional bloqueante), tres órdenes de magnitud más, y lo hace en rutas REST
  directamente alcanzables por el atacante.
- *"El pool del plugin recicla conexiones"*: `MaxIdleConns` por defecto es 2, lo cual solo afecta a
  las conexiones **ociosas** tras la ráfaga; durante la ráfaga las 300 están activas.

---

## DOCS-F2 — Amplificación de memoria sin límite en duplicado de subárbol (C5, high)

**Grupo:** G (DoS)

### Evidencia

1. `server/store/page_hierarchy.go:14-15` — `MaxPageDescendantsLimit = 5000`. Es un límite de
   **filas**, no de bytes.
2. `server/store/page_hierarchy.go:96` — la CTE de descendientes proyecta `pageColListP`, es decir
   **todas** las columnas de la página.
3. `server/store/page_store.go:20-26` — `pageColumnList` incluye `Body` y `SearchText`.
   `server/model/page.go:23,34` — `PageBodyMaxBytes = 2 MiB` y `PageSearchTextMaxBytes = 2 MiB`.
   Por tanto cada fila puede pesar ~4 MiB.
4. `server/store/page_store.go:668-697` (`fetchDescendantRows`) hace `selectAll` de hasta
   `MaxPageDescendantsLimit + 1 = 5001` filas completas en un slice en memoria, **y solo después**
   comprueba el límite de filas (línea 683). No existe ninguna comprobación de tamaño agregado.
5. `server/store/page_duplicate.go:212` lo invoca dentro de `getPageSubtreeForDuplicate`, que además
   corre en una transacción `REPEATABLE READ` con `FOR SHARE` sobre la raíz.
6. `server/app/page.go:303-329` (`buildDuplicatePages` / `clonePageFields`) construye un segundo
   slice de N+1 `*model.Page` que referencia los mismos `Body`/`SearchText`.
7. `server/store/page_duplicate.go:123-138` inserta en *chunks* de 1000 filas; cada chunk se
   serializa como parámetros de un único `INSERT` (hasta ~4 GiB de bind params en el peor caso) y
   viaja **por el driver RPC del plugin** (`shared/driver/conn.go`, `ConnExec` con
   `args []driver.NamedValue`), es decir se codifica también en el proceso del servidor Mattermost.
8. `server/api_page.go:23,175-206` — el handler que dispara todo esto acepta un cuerpo de como
   máximo 4 KiB (`maxPageStructBodyBytes`) y en la práctica ~60 bytes:
   `{"include_children":true}`.

### Cadena de ataque

1. Atacante = empleado autenticado. Crea un space propio (permitido, exclusión MVP).
2. Construye un subárbol grande bajo una página raíz `P`. No necesita llegar al peor caso teórico:
   con 1000 páginas de 500 KiB de body cada una ya son ~500 MiB por copia (y otro tanto de
   `SearchText`, derivado del body). El coste de setup se amortiza porque el propio endpoint de
   duplicado sirve para crecer el árbol: cada `POST .../{page_id}/duplicate` con
   `include_children=true` y `parent_id` = el padre actual **duplica** el tamaño del grupo de
   hermanos, sin aumentar profundidad (`MaxPageDepth = 10` no lo limita), así que se llega al orden
   de miles de páginas en unas decenas de peticiones.
3. Emite N peticiones concurrentes (N = 5-20 basta):
   `POST /plugins/com.mattermost.docs/api/v1/spaces/{S}/pages/{P}/duplicate`
   con cuerpo `{"include_children": true}`.
4. Cada petición aloja simultáneamente: el resultado del scan de `sqlx` (N filas × body+searchtext),
   los clones de `buildDuplicatePages`, y el buffer de parámetros del `INSERT` chunkeado. Con 10
   concurrentes y 500 MiB por copia son ~10-15 GiB de heap sólo en el proceso del plugin, más el
   coste de codificación RPC en el proceso del servidor.
5. Resultado: OOM del proceso del plugin (Docs cae y reinicia en bucle mientras dure el ataque) y/o
   presión de memoria que mata al proceso del servidor Mattermost en el mismo host/contenedor.

### Impacto

C5. El factor de amplificación petición→memoria es de ~60 bytes a varios GiB. Además hay
amplificación de almacenamiento persistente: cada petición trivial escribe un subárbol completo
en `DOCS_Page`.

### Refutaciones consideradas y descartadas

- *"`MaxPageDescendantsLimit` ya lo acota"*: acota filas, no bytes. 5000 × 4 MiB = 20 GiB.
- *"`MovePageToSpace` tiene el mismo patrón"*: no. `collectLiveSubtreeIDs`
  (`server/store/page_move.go:399-422`) proyecta solo `Id, depth`. La asimetría confirma que el
  patrón seguro existe en el repo y que el camino de duplicado se lo saltó.
- *"El atacante necesita subir GiB de contenido primero"*: no, el propio duplicado hace el trabajo
  (paso 2). El setup requiere decenas de peticiones, no miles.
- *"Es contenido propio del atacante"*: irrelevante para C5; el recurso agotado es del servidor.

---

## DOCS-F3 — Space permanentemente inaccesible (C6, high)

**Grupo:** H (integridad) · **Caso bloqueante:** SEC-INT-01

### Evidencia

1. `server/app/space.go:252-288` (`CheckSpaceMembership`) es el único gate de las 24 rutas. Exige
   (a) ser miembro **activo** del equipo (`isActiveTeamMember`, líneas 42-57: un `TeamMember` con
   `DeleteAt != 0` cuenta como no-miembro) y (b) ser miembro del canal de respaldo.
2. `server/app/space.go:365-421` (`RemoveSpaceMember`) sí protege contra el vaciado por la vía del
   propio plugin: `hasOtherAuthorizedMember` (líneas 82-108) exige que quede al menos otro miembro
   del canal que además siga activo en el equipo, y devuelve 409 en caso contrario. El comentario de
   las líneas 365-371 documenta explícitamente la invariante que se quiere sostener: *"a space with
   no reachable member — and every page in it — would be permanently unreachable through the plugin
   API (there is no admin bypass...)"*.
3. **No existe ningún hook que observe la salida de equipo o de canal.** Los únicos métodos
   `func (p *Plugin) X` del repo son `OnActivate`, `OnDeactivate`, `OnConfigurationChange`,
   `ServeHTTP`, `MattermostAuthorizationRequired` y `EnableDocsRequired`
   (`server/plugin.go`, `server/configuration.go`, `server/api.go:79-122`). No hay
   `UserHasLeftTeam`, `UserHasLeftChannel` ni equivalente.
4. No hay ruta de recuperación por API: el canal de respaldo es de tipo `ChannelTypeSpace` ("S"), y
   el propio `pluginapi` documenta que *"The generic Get excludes opaque backing channel types
   (e.g. space)"* (`pluginapi/channel.go:143-146`). Los endpoints REST genéricos de canal de core
   no resuelven canales "S", de modo que ni siquiera un System Admin puede re-añadir un miembro por
   la vía soportada. `AddSpaceMember` (`server/app/space.go:329-363`) exige que el llamante ya sea
   miembro. Solo queda manipulación directa de base de datos.

### Cadena de ataque

Escenario con víctima (pérdida de contenido ajeno):

1. Usuario B crea el space `S` y escribe N páginas. Añade a A (colaboración normal).
2. A, miembro del space, invoca
   `DELETE /api/v1/spaces/{S}/members/{B}`. Permitido por el modelo plano de miembros (exclusión
   MVP). Si hubiera más miembros, A los elimina uno a uno; el guard solo se dispara cuando A sería
   el último.
3. A queda como único miembro autorizado. `RemoveSpaceMember` sobre sí mismo devuelve 409 — el guard
   funciona.
4. A abandona el equipo con una acción **de core**, no de Docs:
   `DELETE /api/v4/teams/{team_id}/members/{A}`. Docs no observa este evento (Evidencia 3).
5. A partir de ese instante `CheckSpaceMembership` falla para todo el mundo: A no pasa
   `isActiveTeamMember`; nadie más está en el canal. Las N páginas de B son irrecuperables por
   cualquier interfaz soportada (Evidencia 4).

Escenario sin atacante, igual de relevante para un MVP interno: *offboarding* normal de un empleado
(un admin lo saca del equipo) deja inaccesible de forma permanente todo space en el que fuera el
último miembro.

### Impacto

C6 — destrucción irreversible de contenido / space permanentemente inaccesible, controlada por el
atacante y afectando a terceros. Es exactamente el caso bloqueante SEC-INT-01 y exactamente la
invariante que el código ya declara querer mantener, de modo que no cae bajo la exclusión MVP
"sin bypass de admin del sistema": el problema no es que falte un bypass, sino que el guard
existente tiene un camino lateral no cubierto.

### Refutaciones consideradas y descartadas

- *"Es la exclusión 'sin bypass de admin del sistema'"*: no. La exclusión cubre "no hay un modo
  admin para leer/escribir cualquier space". Aquí el daño es la pérdida permanente de datos, listada
  aparte como C6 y como caso bloqueante SEC-INT-01.
- *"El atacante se perjudica a sí mismo al salir del equipo"*: el daño a la víctima es permanente y
  el coste para el atacante es reversible (puede volver a unirse al equipo; no recupera el canal,
  pero tampoco lo necesita).
- *"Podría salirse del canal directamente en vez del equipo"*: esa vía sí parece cerrada (los
  endpoints genéricos de canal excluyen el tipo "S"), pero la vía de equipo no lo está.

---

## DOCS-F4 — `Props` de Space y Page sin sanitizar (C1 latente, low) `[DINÁMICO: NO]`

### Evidencia

- `server/model/space.go:35` y `server/model/page.go:68` — `Props mmmodel.StringInterface`, JSON
  arbitrario.
- `server/api_space.go:89,96` y `server/api_page.go:92,99` — el cliente lo fija íntegro vía
  `PATCH /spaces/{id}` y `PATCH /spaces/{id}/pages/{id}`.
- `server/model/space.go:90-124` (`PreSave`/`PreUpdate`) sanitiza `Title`, `Description` e `Icon` con
  `SanitizeUnicode` pero **no toca `Props`**. Lo mismo en `server/model/page.go`. La única
  comprobación es de tamaño: `ValidatePropsSize(..., 64 KiB)` (`space.go:186`, `page.go:258`).
- El `Body`, en cambio, pasa siempre por `ParseTipTapDocument` →`sanitizeTipTapDocument`
  (`server/app/page_content.go:107`, invocado desde `CreatePage`, `UpdatePage`, `UpdatePageDraft` y
  ambos caminos de publish). La asimetría es el finding.
- `Props` se devuelve verbatim a todos los miembros del space en `GET /spaces/{id}` y
  `GET /spaces/{id}/pages/{page_id}`.
- Hoy no hay sink: `webapp/src/**` no contiene `dangerouslySetInnerHTML`, `innerHTML` ni ningún
  `href`/`src` derivado de datos de servidor (el único `href` es
  `webapp/src/components/menu/menu.tsx:56`, alimentado por constantes en
  `spaces_sidebar_header.tsx:49-50`), y el editor es un stub (`page_editor.tsx`).

### Por qué es low y no informational

Es un canal de almacenamiento de contenido arbitrario controlado por el cliente que se entrega a
otros usuarios y que, a diferencia del `Body`, no tiene ninguna barrera de sanitización. El MVP
monta el editor real del host (`hostGetEditor()`), de modo que la superficie de renderizado va a
crecer justo en la ventana de lanzamiento. El coste del fix (aplicar el mismo tratamiento de valores
que ya existe para atributos TipTap, o restringir `Props` a un esquema conocido) es bajo comparado
con el de auditarlo después.

---

## DOCS-F5 — `sanitizeURL` deja pasar referencias sin esquema (low) `[DINÁMICO: NO]`

### Evidencia

- `server/model/page_content.go:567-583` — `sanitizeURL` devuelve la URL **sin tocar** cuando
  `decodeURLScheme` reporta `hasScheme == false`, y `urlScheme`
  (`page_content.go:509-526`) devuelve `("", false)` en cuanto encuentra `/`, `?` o `#` antes de
  los dos puntos. Por tanto `//evil.example/x` es una referencia relativa a efectos del sanitizador
  y sobrevive intacta en `href`, `src`, `poster` y `xlink:href`.
- El sanitizador es por lo demás sólido: se verificó el manejo de mayúsculas, entidades HTML
  (`html.UnescapeString`), caracteres de control (`urlStripChars`, superconjunto de lo que quita el
  navegador según WHATWG), el doble paso de *strip*, el *sniffing* de `data:` (`isBase64ImagePayload`
  decodifica y comprueba la firma real, no solo el MIME declarado) y la lista blanca de nodos/marcas.
  No se encontró bypass de ejecución de script.

### Impacto

No es XSS. Sí es: (a) *open redirect* fuera de origen en enlaces de páginas, y (b) carga de recursos
externos (`<img src="//attacker/px">`) desde documentos internos, que filtra IP, User-Agent y el
hecho de que un documento concreto ha sido abierto — relevante en un despliegue con requisitos de
tipo FedRAMP. La corrección es acotada: rechazar `//` inicial en claves de URL.

---

## Descartados por baja severidad (informational — no pasan a Fase 2)

| ID | Observación | Por qué no pasa |
|----|-------------|-----------------|
| I-01 | `Draft.FileIds` no valida propiedad del fichero, solo el formato del ID (`server/model/draft.go:175-185`) | Los IDs no se dereferencian nunca: no se resuelven a `FileInfo`, no se sirven, y no se transfieren a la página al publicar (`FileIds` solo aparece en `draft_store.go`; `Page` no tiene el campo). Los drafts solo son legibles por su propietario (`GetPageDraft` filtra por `userID`). Deuda de validación, sin impacto hoy. |
| I-02 | `maxDraftBodyBytes = 6 × PageBodyMaxBytes + 64 KiB ≈ 12,6 MiB` por autosave (`server/api_page_drafts.go:15-23`) | El factor 6× está justificado por el escapado JSON y el body decodificado se recorta a 2 MiB en `normalizeContentToDoc`. La amplificación petición→memoria es ~2×, no da un DoS práctico frente a DOCS-F2. |
| I-03 | Ausencia total de audit logging pese a implementar `Auditable()` en `Space`, `Page` y `Draft` | No es una vulnerabilidad explotable. Relevante para SOC2/FedRAMP, no para el MVP interno. |
| I-04 | `Space.Icon` solo se valida por tamaño (256 bytes) y `SanitizeUnicode`; nada garantiza que sea un emoji | Hoy se renderiza como texto (escapado por React). Cubierto conceptualmente por DOCS-F4. |
| I-05 | Cualquier miembro de un space puede añadir miembros al canal de respaldo con privilegios elevados del plugin, incluso un *guest* (`server/app/space.go:329-363`) | El efecto está confinado al canal tipo "S", invisible para las APIs y la UI genéricas de core, y `AddSpaceMember` valida pertenencia activa al equipo del target. No hay impacto fuera del ámbito Docs (no es C3). Además, el modelo plano de miembros es exclusión MVP. |
| I-06 | `p.router` / `p.service` se leen sin sincronización en `ServeHTTP` y son `nil` si `OnActivate` no completó (`server/plugin.go:98-108`, `server/api.go:79-81`) | Core no enruta peticiones a un plugin no activado; no se encontró camino alcanzable. |
| I-07 | `ListSpaceMembers` calcula el índice de sonda como `(page+1)*perPage` con `page` acotado a 2^20 (`server/app/space.go:317`) | Requeriría un canal con cientos de millones de miembros para producir un OFFSET real. No explotable. |

---

## Grupos auditados sin findings (verificado, no asumido)

- **Grupo A — frontera de autenticación (SEC-AUTH-01, SEC-AUTH-04).** `MattermostAuthorizationRequired`
  (`server/api.go:86-96`) confía en la cabecera `Mattermost-User-ID`. Se verificó en el código de
  core que `ServePluginRequest` **borra** la cabecera entrante y la reescribe a partir de la sesión
  validada, y que el CSRF de peticiones a plugins se resuelve en esa misma capa. La vía
  inter-plugin (`PluginHTTP`) sí permite fijar la cabecera, pero solo la pueden usar plugins
  instalados, que el runbook clasifica como componentes *trusted*. Las 24 rutas están bajo el
  middleware; no hay ninguna ruta sin gate.
- **Grupo B — IDOR (SEC-IDOR-02, SEC-IDOR-06).** Todos los handlers de página propagan el
  `space_id` de la URL a la capa de store (`GetPageInSpace`, `UpdatePage`, `DeletePage`,
  `RestorePage`, `MovePage`, `GetPageActiveEditors`, ...), de modo que un `page_id` ajeno con un
  `space_id` propio devuelve 404. `move-to-space` y `duplicate` exigen membresía en **ambos** spaces
  (`resolveTargetSpace`, `server/api_page.go:31-37`) y además rechazan el cruce entre equipos
  (`server/app/page.go:250-252`). Los drafts se leen siempre por `(userID, spaceID, pageID)`.
- **Grupo C — confused deputy (SEC-DEPUTY-02).** `Space.ChannelId` es `json:"-"`
  (`server/model/space.go:29`), `CreateSpace` rechaza un `ChannelId` no vacío
  (`server/app/space.go:136-138`) y `SpacePatch` no lo incluye. Toda operación privilegiada de canal
  usa el `ChannelId` leído de la fila del space, nunca de la petición, y las resoluciones pasan por
  `GetChannelOfType(..., ChannelTypeSpace)` (`server/app/space.go:531,688`). Hay además una
  restricción de unicidad `uq_docs_space_channel_id`. No se puede archivar un canal no-space.
- **Grupo D — sanitizador TipTap (SEC-XSS-02, SEC-XSS-05).** Ver DOCS-F5 para el detalle de lo
  verificado. Lista blanca estricta de nodos y marcas, `stripDangerousKeys` aplicado tanto a `attrs`
  como al propio objeto de nodo/marca, recursión acotada (`maxTipTapDepth = 100`,
  `maxTipTapNodes = 50 000`) y con *fail-closed* al superarla, y re-serialización del documento
  saneado antes de almacenar (lo que elimina claves duplicadas y descarta claves de nivel superior
  no modeladas, cerrando los ataques de *parser differential*). Todos los caminos de escritura de
  `Body` pasan por él.
- **Grupo E — SQL injection (SEC-SQLI-01).** Todo se construye con `squirrel` (placeholders `$N`) o
  con SQL crudo parametrizado y pasado por `Rebind`. Los cuatro `fmt.Sprintf` sobre SQL
  (`page_hierarchy.go:40,55,79`, `page_store.go:674`, `page_move.go:402`, `draft_store.go:146`)
  interpolan exclusivamente constantes de compilación (`MaxPageHierarchyDepth`, `MaxPageDepth`,
  `MaxPageDescendantsLimit`). El único `UPDATE ... FROM (VALUES ...)` construido a mano
  (`page_move.go:214-232`) toma sus IDs de un `SELECT ... FOR UPDATE` previo y liga cada valor como
  parámetro.
- **Grupo F — fuga de información.** `writeAppError` limpia `DetailedError` antes de serializar
  (`server/api.go:128-139`) y solo registra en log los 5xx. `Space.ChannelId` y `Page.ChannelId` son
  `json:"-"`. Los eventos WebSocket de miembro eliminado se envían al usuario afectado, no al canal.
- **Grupo G — CTEs recursivas (SEC-DOS-03).** Las tres CTEs acotan profundidad
  (`MaxPageHierarchyDepth = 50`) y rompen ciclos explícitamente con un array `path` /`id_path`
  (`server/store/page_hierarchy.go:40-100`), y los `LIMIT` se aplican en SQL. El vector de DoS real
  no está en la recursión sino en el volumen de bytes por fila: ver DOCS-F2.
- **Grupo I — feature flag.** `snapshotFeatureFlags` (`server/configuration.go:73-75`) falla cerrado
  (`cfg == nil` → deshabilitado) y se siembra en `OnActivate` antes de servir.
- **Grupo K — webapp.** No hay sinks XSS (`dangerouslySetInnerHTML`, `innerHTML`, `eval`,
  `insertAdjacentHTML`: cero coincidencias en `webapp/src/**`). El único `href` dinámico
  (`menu.tsx:56`) recibe constantes. La fuente de datos sigue siendo un mock
  (`webapp/src/data/docs_data_source.ts`) y el editor es un stub, así que hoy no se renderiza
  contenido del backend.
- **Grupo L — supply chain.** Fuera del alcance práctico de esta pasada (no hay lockfile anómalo ni
  dependencias con `replace` sospechosos en `go.mod`).

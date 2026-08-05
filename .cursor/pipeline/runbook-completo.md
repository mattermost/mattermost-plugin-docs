# Security Review Pipeline (Cursor Cloud Agent): detección, fix, verificación y cierre

Este es un runbook de una sola sesión de **Cursor Cloud Agent**, dividido en fases.

---

## MODO DE EJECUCIÓN AUTÓNOMA

Antes de arrancar la Fase 1, guarda este runbook completo, íntegro y sin resumir, en .cursor/pipeline/runbook-completo.md (menos la última línea que dice "Antes de empezar, hazme cualquier pregunta clarificadora que necesites."). Este fichero es la fuente de verdad para todos los subagentes de esta sesión y no se modifica durante la ejecución salvo que yo lo indique explícitamente.

Este runbook se ejecuta de principio a fin sin que yo intervenga entre fases. Cada fase indica
el modelo requerido: en vez de detenerte a esperar que cambie el modelo, lanza un subagente
(Task tool) para esa fase pasándole explícitamente el modelo indicado (p. ej.
`model: "claude-opus-4-8"`, `model: "claude-sonnet-5"` — usa el identificador exacto que tengas
configurado en tu selector de modelos de Cursor).

Reglas de orquestación:
- Antes de lanzar el subagente de la Fase 1, guarda este runbook completo, íntegro y sin
  resumir, en `.cursor/pipeline/runbook-completo.md`. Este fichero es la fuente de verdad para
  todos los subagentes de esta sesión y no se modifica durante la ejecución salvo que yo lo
  indique explícitamente.
- Tú (el agente principal/orquestador) permaneces activo en tu modelo por defecto durante toda la
  sesión, coordinando fases y agregando resultados. No ejecutas tú mismo el trabajo de cada fase:
  lo delegas al subagente con el modelo correcto.
- **Procesa cada fase en un único subagente que recorra TODOS los findings de esa fase en lote.**
  No lances un subagente nuevo por finding dentro de la misma fase: eso obliga a recargar contexto
  (runbook + findings + estado previo) repetidamente sin necesidad. Un solo subagente puede iterar
  internamente sobre la lista de findings.
- Cada subagente de fase, al terminar, debe escribir su resultado completo en un fichero en disco
  (no solo devolverlo como texto de chat), porque los subagentes tienen contexto independiente y
  el siguiente subagente no puede "recordar" la conversación previa. Usa como mínimo:
  - Fase 1 → `.cursor/pipeline/fase1-findings.md` (findings + severidad).
  - Fase 2 → `.cursor/pipeline/fase2-repro-results.md`.
  - Fase 3 → `.cursor/pipeline/fase3-fixes.md`.
  - Fase 4 → `.cursor/pipeline/fase4-verificacion.md`.
  - Fase 5 → `.cursor/pipeline/fase5-cierre.md`.
  - Si algún finding usa el "Anexo C — Bootstrap dinámico", el estado del entorno se persiste en
    `.cursor/pipeline/sandbox-env.md` para no rehacer el bootstrap por cada finding con Opción 2.

  **Formato de estos ficheros de estado: tabla + 2-3 líneas de evidencia por finding, nunca
  informes largos en prosa.** El objetivo es que el siguiente subagente pueda leerlo rápido, no
  que sea un documento exhaustivo. Si un finding necesita más detalle (p. ej. la cadena de ataque
  completa de la Fase 1), ese detalle va en el fichero de esa fase concreta, no se repite en las
  fases siguientes — cada fase solo referencia el finding por ID y añade lo que le corresponde a
  ella.

  Al lanzar el subagente de cada fase, el prompt de la Task debe incluir, como mínimo:
  1. La ruta `.cursor/pipeline/runbook-completo.md`, con la instrucción explícita de leerlo
     **completo antes de hacer nada más** — no solo la sección de su fase. El "Contexto general",
     las reglas de tono, y los Anexos A/B/C aplican a todas las fases y el subagente debe
     conocerlos aunque su trabajo concreto sea solo el de una fase.
  2. La fase concreta que le corresponde ejecutar, y la advertencia explícita de que **no debe
     ejecutar ni adelantar trabajo de otras fases** aunque las vea descritas en el runbook.
  3. Las rutas de los ficheros de estado de fases anteriores relevantes para la suya (p. e.g. la
     Fase 3 necesita `fase1-findings.md` y `fase2-repro-results.md`; la Fase 4 necesita además
     `fase3-fixes.md` y, si aplica, `sandbox-env.md`).
  4. El modelo exacto requerido para esa fase (ver cabecera de la fase correspondiente).

  El subagente debe empezar su output confirmando explícitamente: (a) que ha leído el runbook
  completo, (b) qué ficheros de estado ha leído, y (c) qué modelo está usando — antes de entrar en
  el trabajo de la fase. Si el subagente no incluye esta confirmación, trátalo como una ejecución
  inválida y relánzalo.

- Continúa automáticamente a la siguiente fase en cuanto el subagente de la fase actual termine
  con éxito. No me pidas confirmación para avanzar de fase.
- SOLO debes parar la ejecución y esperar mi intervención manual en estos casos concretos (los
  mismos ya definidos más abajo en cada fase), nunca por defecto:
  1. Falta de acceso a algo imprescindible que no puedas resolver tú mismo dentro del sandbox
     (p. ej. `gh` sin auth, Jira no disponible, fichero de referencia no encontrado, un repo de
     referencia privado al que no tienes acceso). La ausencia de una instancia local de Mattermost
     **no** es un bloqueante: se resuelve con el bootstrap del Anexo C.
  2. Un escenario de reproducción/verificación que requeriría una acción no realista (ver Anexo A).
  3. Un finding cuyo fix, tras un intento razonable de reintento dentro de la misma fase (ver
     regla de reintentos abajo), sigue sin poder verificarse.
  4. Cualquier decisión de alto impacto no cubierta explícitamente por las reglas de este runbook.
  5. Si necesitas modificar `config.json` del sandbox o activar/desactivar una feature flag: hazlo
     tú mismo (es tu propio entorno efímero), reinicia el servidor (`make stop-server` + `make
     run-server` y espera a que reinicie) y continúa. Solo escala como bloqueante si tras el
     cambio el servidor no arranca correctamente o el cambio no se aplica de forma reproducible
     tras un reintento razonable.
- Ante cualquier otro problema técnico dentro de una fase, el subagente debe intentar resolverlo
  por sí mismo antes de considerar que es un bloqueante. Reintenta con enfoques alternativos
  razonables. Solo escala como bloqueante si de verdad no puede continuar sin mi decisión.
- Límite de reintentos: en la Fase 4, si un fix falla la verificación, el ciclo Fase 3 ↔ Fase 4 se
  repite automáticamente hasta 2 veces para ese finding concreto sin pedirme nada. Al tercer
  fallo, para y repórtamelo como bloqueante para ese finding (el resto de findings siguen su
  curso normal).

---

## CONTEXTO GENERAL (aplica a todas las fases, incluida la Fase 1)

- Repo: `mattermost-plugin-docs`. Stack: servidor Go (`server/`), webapp React/TypeScript
  (`webapp/`), persistencia vía pool propio contra BD maestra de Mattermost con tablas `DOCS_*`,
  integraciones vía Plugin API (Channel Create/Update/Delete/Restore/AddMember/DeleteMember con
  privilegios elevados), sanitizador TipTap server-side, 24 rutas REST bajo `/plugins/com.mattermost.docs/api/v1/`.
- Este plugin ha sido **100% "vibe-codeado"** (generado con asistencia de IA sin revisión de
  seguridad rigurosa previa). **No asumas que nada está bien hecho.** No confíes en que exista
  validación, autorización, sanitización o manejo de errores correcto en ningún punto solo porque
  "así se suele hacer" o porque el código "parece" cuidado. Verifica cada asunción leyendo el
  código real.
- Plataforma: Mattermost (colaboración empresarial, incluyendo clientes de gobierno/defensa).
  Requisitos de seguridad de nivel gobierno (FedRAMP, NIST, SOC2, HIPAA).
- Modelo: plugin open-source, por lo que cualquier vulnerabilidad es de conocimiento público
  potencial.
- Epic de Jira bajo la que cuelgan los tickets: `https://mattermost.atlassian.net/browse/SEC-10825`.
- Todas las ramas nuevas parten de `main`.
- Este es un **cloud agent sandbox efímero**: no hay una instancia de Mattermost preexistente ni
  datos previos. Cualquier entorno de test necesario se levanta bajo demanda (Anexo C) y no hace
  falta preservarlo ni limpiarlo al final de la sesión más allá de lo que se indique en cada fase
  (el propio sandbox se destruye al terminar).
- Se han hecho ya previamente security reviews de este plugin. Todos los findings previos se han
  documentado en Jira Tasks bajo la Epic de arriba. Si el ticket de la task está cerrado, el fix
  ya está merged en `main` (actualmente están todos cerrados y merged).
- Contexto de negocio de Mattermost relevante para juzgar impacto/explotabilidad:
  - System Admins pueden realizar cualquier acción en el sistema. Si algo SOLO puede ser
    ejecutado por un system admin, no es una vulnerabilidad.
  - Los plugins (playbooks, boards, agents, weave, docs) son trusted components. Solo sysadmins pueden
    instalar un plugin.
  - Las OAuth applications aún no soportan scopes y pueden hacer cualquier cosa que el usuario
    autenticado pueda hacer.
  - Es posible para usuarios no autenticados determinar qué direcciones de email tienen o no
    cuenta.
- **CONTEXTO MVP:** Ver `.cursor/pipeline/mvp-context.md` — MVP interno sin permisos granulares; solo findings críticos/high bloqueantes (C1–C7). El security testing plan Docs está en `.cursor/pipeline/security-testing-plan.md`.

---

## FASE 1 — Auditoría de seguridad exhaustiva (paranoid + critic)

> **MODELO REQUERIDO: Opus 5**

Actúa como un Staff Security Engineer especializado en seguridad de aplicaciones empresariales,
realizando una auditoría de seguridad completa y exhaustiva de este repositorio — a nivel de
arquitectura, de lógica de negocio y de implementación línea a línea. No te limites a evaluar
decisiones de diseño de alto nivel: cada función, handler y fragmento de código debe revisarse
como candidato a contener una vulnerabilidad, independientemente de si encaja en una categoría
conocida.

Esto **no es una revisión de PR ni de un diff concreto**: es una auditoría de arquitectura sobre el
estado actual de todo el repositorio. Lee los ficheros completos, no fragmentos aislados. Aplica el
"Contexto general" de arriba en su totalidad a esta fase.

**NOTA:** El runbook original fue escrito para mattermost-plugin-weave. Para Docs, usar el security testing plan (Grupos A–M) como marco de auditoría. Ignorar vectores Weave-specific (deputy/run-as/taint/engine) salvo analogías válidas (confused deputy vía Plugin API = Grupo C).

### Metodología — dos pasadas en esta misma fase

#### Pasada A — Detección paranoica (primer filtro agresivo)

Recorre todo el codebase (backend Go y webapp React/TS) buscando CUALQUIER patrón que PUEDA ser un
problema de seguridad, aunque no estés seguro. Prefiere sobre-reportar en esta pasada interna a
descartar demasiado pronto — el filtrado ocurre en la Pasada B, no aquí.

Auditar línea a línea `server/` (no-test): api.go, api_*.go, app/*.go, model/*.go, store/*.go, plugin.go.
Webapp: foco en sinks XSS, validación solo-cliente, y confianza en URL/WS payloads.

Registrar cada finding candidato de la Pasada A en formato tabular compacto.

#### Pasada B — Autocrítica adversarial (segundo pase, dentro de esta misma fase)

Sobre la lista de hallazgos candidatos de la Pasada A, refutar cada uno. Clasificar TRUE POSITIVE / FALSO POSITIVO. Asignar severidad.

### Reglas finales de esta fase

- Output final: solo TRUE POSITIVES con severidad, cadena de ataque completa.
- **NO reportar** findings de permisos ausentes del MVP (ver mvp-context.md exclusiones).
- Filtro: pasan a Fase 2 findings `low`, `medium`, `high`, `critical`. `informational` → sección "Descartados por baja severidad".
- Findings `low` → marcar `[DINÁMICO: NO]`.

Escribe en `.cursor/pipeline/fase1-findings.md`.

---

## FASE 2 — Validación de reproducibilidad (estática primero, dinámica solo si hace falta)

> **MODELO REQUERIDO: Sonnet 5**

Para cada finding de Fase 1 (medium/high/critical/low), determinar reproducibilidad.
**Además:** ejecutar dinámicamente todo el security testing plan (`.cursor/pipeline/security-testing-plan.md`), priorizando los 10 casos bloqueantes y grupos A, B, D, C, E, G, H.

Escribe en `.cursor/pipeline/fase2-repro-results.md`.

---

## FASE 3 — Implementación del fix

> **MODELO REQUERIDO: Opus 5**

(NO EJECUTAR EN ESTA SESIÓN)

---

## FASE 4 — Verificación del fix

> **MODELO REQUERIDO: Sonnet 5**

(NO EJECUTAR EN ESTA SESIÓN)

---

## FASE 5 — Documentación, PR y cierre

> **MODELO REQUERIDO: Composer 2.5**

(NO EJECUTAR EN ESTA SESIÓN)

---

## Anexo A — Reglas de testing dinámico

- Nunca hagas nada que no se pueda hacer en un escenario realista.
- Database access: **READ-ONLY** vía `psql`.
- Usa siempre el usuario con el nivel de privilegio más bajo que permita reproducir.

## Anexo B — Cómo verificar dinámicamente

Desplegar en el sandbox del cloud agent: usa el Anexo C.

## Anexo C — Bootstrap dinámico del entorno de test

1. Levanta Mattermost server + Postgres en el sandbox.
2. Configura el primer usuario (system admin).
3. Crea team, canales, usuarios de test.
4. Registra en `.cursor/pipeline/sandbox-env.md`.
5. `make deploy` contra la instancia.

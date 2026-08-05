# Docs Plugin Security Test Plan (MVP interno)

Ver mvp-context.md para exclusiones y criterios C1-C7.

## 10 casos bloqueantes
1. SEC-AUTH-01 - forjar Mattermost-User-ID
2. SEC-AUTH-04 - CSRF mutaciones
3. SEC-XSS-02 - bypass allowlist sanitizador
4. SEC-XSS-05 - bypass sanitizeURL
5. SEC-IDOR-02 - leer pagina otro space via space_id propio + page_id ajeno
6. SEC-IDOR-06 - move/duplicate cross-space
7. SEC-DEPUTY-02 - archivar canal no-space
8. SEC-SQLI-01 - SQLi CTEs
9. SEC-DOS-03 - DoS CTE recursiva
10. SEC-INT-01 - space permanentemente inaccesible

## Grupos: A (auth), B (IDOR), C (deputy/Plugin API), D (XSS TipTap), E (SQLi), F (leak), G (DoS), H (integridad), I (feature flag), J (files), K (webapp), L (supply chain), M (audit)

## Exclusiones MVP (NO findings)
- No ACLs por pagina
- Modelo plano miembros
- Cualquier miembro borra/restaura space
- force last-write-wins
- Sin bypass admin sistema
- Cualquier miembro equipo crea spaces

## Superficie
- 24 rutas REST server/api.go
- Auth: Mattermost-User-ID header server/api.go:86
- Gate unico: CheckSpaceMembership server/app/space.go
- Sanitizer: sanitizeTipTapDocument server/model/page_content.go
- Plugin API privilegiada: Channel ops server/app/space.go

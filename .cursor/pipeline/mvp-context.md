# Contexto MVP — Security Review mattermost-plugin-docs

**IMPORTANTE para todos los subagentes de esta sesión:**

1. El equipo va a lanzar una versión **MVP en Agosto** que **no tendrá nada relacionado con permisos** (ACLs por página, roles de space, etc.). Eso es decisión de producto, no un finding de seguridad.

2. **Objetivo de esta revisión:** NO detectar findings de "faltan permisos aquí". SÍ detectar cualquier finding **crítico o high** de seguridad que deba solucionarse **sí o sí** antes del lanzamiento del MVP para dogfooding interno.

3. El MVP se lanzaría **únicamente de forma interna** (solo empleados), pero aún hay que comprobar que no haya vulnerabilidades que puedan afectar seriamente a los empleados o comprometer confidencialidad/integridad/disponibilidad de forma **crítica**.

4. El **security testing plan** de referencia es de **Session Attributes** (feature distinta). Este análisis es del **plugin Docs** (`com.mattermost.docs`). Usar el plan de pruebas Docs incluido en el runbook/sesión, no el de Session Attributes.

5. El runbook reutiliza un prompt de **mattermost-plugin-weave**. Ignorar referencias a Weave (deputy, run-as, taint, engine_run, etc.) que no aplican a Docs. La superficie real de Docs está documentada en el security testing plan (24 rutas REST, CheckSpaceMembership, sanitizador TipTap, Plugin API con privilegios elevados, etc.).

6. **Exclusiones explícitas del MVP** (NO son findings): sin ACLs por página, modelo plano de miembros, cualquier miembro puede borrar/restaurar space, force last-write-wins, sin bypass de admin del sistema, cualquier miembro del equipo puede crear spaces.

7. **Criterios bloqueantes (C1–C7):**
   - C1: XSS / robo de sesión
   - C2: Leer/escribir content de space ajeno
   - C3: Afectar datos/canales fuera del ámbito Docs
   - C4: SQL arbitrario
   - C5: DoS del servidor completo
   - C6: Destrucción irreversible / space inaccesible permanentemente
   - C7: Bypass de autenticación o gate de membresía

8. **Alcance de esta sesión:** Solo Fases 1 y 2. NO implementar fixes (Fases 3–5).

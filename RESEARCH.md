## Veredicto

Sí, **la tesis central tiene mucho sentido** y el documento funciona muy bien como mapa del estado del arte. Pero para convertirlo en una investigación defendible, un producto diferenciable o una arquitectura construible, **prefiero un scope más estrecho y con otro centro de gravedad**:

> **Multiversos no debería presentarse principalmente como un VCS “post-Git”, sino como un plano de control nativo en evidencia para cambios especulativos producidos por agentes, compatible con Git.**

La formulación más potente sería:

> **Git versiona la historia aceptada. Multiversos versiona las posibilidades todavía no aceptadas y la evidencia utilizada para decidir cuál pasa a la historia.**

No cambiaría el problema que identificaste. Cambiaría **la unidad de innovación**: de “almacenamiento y merge de código” a **decisión verificable sobre alternativas de código**.

---

# 1. Lo mejor del scope actual

Hay cuatro decisiones que conservaría casi intactas.

Primero, el bucle:

\[
\text{intent} \rightarrow N\text{ mundos} \rightarrow \text{evidencia} \rightarrow \text{decisión} \rightarrow \text{admisión}
\]

Ese es el corazón real y es más interesante que cualquiera de los componentes individuales.

Segundo, la compatibilidad con Git. No veo una razón inicial para reemplazar el formato de intercambio que ya conecta repositorios, forges, CI, revisión y despliegue. La estrategia correcta es utilizar Git como plano de interoperabilidad, no necesariamente como modelo conceptual interno.

Tercero, hacer que **los oráculos sean más autoritativos que los agentes**. El modelo puede proponer código, pruebas y explicaciones, pero no debe poder autodeclararse “correcto”, “revisado” o “listo para merge”. El reciente trabajo Proof-or-Stop formaliza precisamente esta separación entre una afirmación emitida por el agente y evidencia fresca, ligada al estado exacto del código, que permite avanzar el ciclo de vida. 

Cuarto, relegar el almacenamiento semántico estilo Unison. Unison demuestra que el content-addressing por definición es extremadamente poderoso cuando el lenguaje, el AST, el sistema de tipos y la resolución de nombres se diseñan conjuntamente. Eso no se traslada directamente a un repositorio políglota mediante hashes de árboles de tree-sitter. 

---

# 2. La distinción conceptual más importante

El documento usa “multiversos”, “torneo”, “merge especulativo” y “agentes paralelos” como si describieran el mismo problema. En realidad hay **tres regímenes diferentes**, con operaciones distintas:

| Régimen | Relación entre cambios | Operación correcta |
|---|---|---|
| Variantes de una misma solución | Sustitutivas: sólo debe sobrevivir una | **SELECT** |
| Trabajos distintos sobre el mismo producto | Complementarios o parcialmente solapados | **COMPOSE / SERIALIZE / REPAIR** |
| Cambios ya aceptables esperando llegar a trunk | Secuenciales y sujetos a interacción | **SPECULATE / RETEST / ADMIT** |

### A. Selección entre alternativas

Un intent produce ocho implementaciones distintas. No quieres mergearlas: quieres **escoger una** o, eventualmente, sintetizar una nueva a partir de partes de varias.

Esto es lo que valida CodeMonkeys: muchas trayectorias producen candidatos y un mecanismo de selección basado en pruebas y un selector final escoge uno. El resultado de 66,2% del ensemble demuestra que una buena selección puede superar a cada integrante por separado. 

### B. Composición de trabajos distintos

Un agente modifica autenticación y otro cambia el modelo de datos. Ambos cambios pueden ser necesarios. Aquí no existe “ganador”; debes decidir si son compatibles, en qué orden integrarlos y qué premisas quedaron obsoletas.

Claim Plane aborda la admisión previa de intents con base, recursos, dependencias y alcance declarado. CoAgent aborda concurrencia, notificaciones de invalidación y reparación selectiva. Son problemas de coordinación y serialización, no best-of-N. 

### C. Admisión especulativa a trunk

Zuul y GitHub merge queue no seleccionan entre implementaciones alternativas. Especulan sobre el orden en que varios cambios serán incorporados y ejecutan pruebas sobre el estado que resultaría de esa secuencia. 

**Conclusión:** Multiversos necesita al menos dos motores conceptualmente separados:

1. **Candidate Race:** genera y selecciona entre soluciones sustitutivas.
2. **Integration Engine:** compone, serializa y admite cambios complementarios.

En el MVP y en el primer paper trabajaría exclusivamente en el primero, más el gate final. La composición concurrente sería una segunda línea de investigación.

---

# 3. La afirmación de novedad debe estrecharse

Al **12 de agosto de 2026**, ya no usaría:

> “Nadie los ha unificado todavía”.

Es una afirmación negativa demasiado absoluta y el espacio se ha movido rápidamente durante 2026:

- **Fork, Explore, Commit** ya define contextos de branch con filesystem y procesos aislados, copy-on-write, commit/abort y first-commit-wins. 
- **Claim Plane** ya hace del `ChangeIntent` versionado un objeto previo a las escrituras y gobierna alcance, dependencias y admisión. 
- **Proof-or-Stop** ya implementa lifecycle gates basados en evidencia fresca y ligada al estado fuente. 
- **CoAgent** ya trata la coordinación de agentes como un problema de concurrency control y reparación selectiva. 
- **GitButler** ya enlaza sesiones de agentes con ramas paralelas, registra sus cambios y ofrece un CLI orientado explícitamente a agentes. 

La formulación defendible sería:

> **Hasta donde alcanza el corpus revisado, no existe un sistema públicamente documentado que combine, en un solo plano de control Git-compatible: intents versionados, exploración de implementaciones alternativas, asignación adaptativa de cómputo de verificación, evidencia ligada al estado exacto del candidato y admisión del estado integrado resultante.**

Esa intersección todavía parece diferenciada. Pero debes definir el corpus, la fecha de corte y qué significa exactamente “combinar”.

---

# 4. El scope que recomiendo

## Nombre técnico

**Multiverses: An Evidence-Native Control Plane for Speculative Software Change**

O en español:

**Multiversos: plano de control nativo en evidencia para cambios especulativos de software**

“Más allá de Git” puede seguir siendo una narrativa o título de divulgación, pero no lo usaría como descripción técnica principal. Invita a que el lector evalúe el proyecto como sustituto de Git, mientras que la innovación real está más arriba.

## Pregunta de investigación central

> **Bajo un presupuesto total fijo, ¿puede un scheduler consciente de evidencia asignar dinámicamente cómputo entre generación, pruebas y desafío adversarial para admitir cambios con mayor corrección real y menor tasa de falsos positivos que un agente único, un best-of-N fijo o un selector basado sólo en tests?**

Ésta es una pregunta concreta, medible y diferenciada.

## Lo que entra en el núcleo

1. Intent versionado y ligado a un estado base.
2. Mundos aislados con código, ambiente y lineage.
3. Generación de candidatos heterogéneos.
4. Ledger de evidencia ligado criptográficamente al mundo exacto.
5. Scheduler adaptativo de candidatos y oráculos.
6. Selección bajo hard gates y objetivos múltiples.
7. Revalidación contra el estado exacto que llegaría a trunk.
8. Emisión de un commit Git ordinario y una decisión auditable.

## Lo que dejaría fuera del núcleo

- Nuevo formato de almacenamiento de código.
- Teoría propia de patches.
- Identidad content-addressed de funciones políglotas.
- CRDT de código.
- Resolución semántica general de merges.
- Infraestructura propia de microVMs.
- Glean como dependencia obligatoria.
- Provenance público completo con Sigstore.
- Coordinación general de múltiples intents simultáneos.
- UI de escritorio.
- Reemplazo de GitHub o de las forges.

Todo eso puede permanecer como visión de largo plazo o adapters, pero no como obligaciones del primer sistema.

---

# 5. El objeto fundamental no debe ser una branch

Definiría cuatro primitivas y una decisión:

```text
Intent =
    base_state
  + specification
  + preconditions
  + postconditions
  + constraints
  + budget
  + admission_policy

World =
    parent_worlds
  + code_tree_digest
  + environment_digest
  + agent/model/policy identity
  + context digest
  + patch/change
  + execution trace
  + lineage

Evidence =
    world_digest
  + oracle identity/version
  + oracle inputs
  + result/artifacts
  + producer identity
  + freshness
  + cost
  + trust level

Decision =
    SELECT | COMPOSE | SERIALIZE | REPAIR | REJECT | ADMIT

Attestation =
    intent digest
  + selected world digest
  + evidence digests
  + policy version
  + decision
  + signer identity
```

Un `World` no puede ser únicamente un commit o un `jj change`. El comportamiento observable depende también de paquetes instalados, servicios, base de datos, variables, filesystem y procesos. El trabajo Fork, Explore, Commit llega a la misma conclusión: la exploración de agentes requiere aislar tanto estado de filesystem como estado de procesos. 

Esto conduce a una arquitectura más clara:

- **Data plane:** código, snapshots, artefactos y CAS.
- **Execution plane:** worktrees, contenedores o microVMs.
- **Control plane:** intents, scheduler, worlds y decisiones.
- **Evidence plane:** oráculos, receipts y freshness.
- **Trust plane:** firmas, identidades, políticas y attestations.
- **Knowledge plane:** índices SCIP/Glean opcionales.

Tus seis pilares actuales pasan a ser implementaciones o adapters de estos planos, en vez de seis contribuciones centrales simultáneas.

---

# 6. Dónde está realmente la innovación algorítmica

Crear ocho worktrees y ejecutar ocho agentes ya no es novedoso. Tampoco lo es escoger el candidato con más tests verdes.

La parte investigable es un **scheduler de valor de información**:

\[
a_t =
\arg\max_a
\frac{
\mathbb{E}[\Delta \text{calidad de decisión}\mid a]
}{
\operatorname{coste}(a)
}
\]

En cada paso, la acción \(a\) puede ser:

- generar otro candidato;
- mutar un candidato prometedor;
- ejecutar un test barato;
- ejecutar una suite cara;
- generar propiedades;
- lanzar mutation testing;
- pedir un ataque adversarial;
- comparar dos candidatos diferencialmente;
- detener la exploración y admitir;
- detenerla y escalar a humano.

Esto supera el patrón fijo de “N mundos y luego ranking”. El sistema aprende cuándo:

- hay poca diversidad y conviene generar otra familia de solución;
- dos candidatos son funcionalmente equivalentes;
- una señal adicional no cambiará la decisión;
- un candidato domina el Pareto frontier;
- el riesgo residual continúa siendo demasiado alto;
- el valor esperado de otra prueba es inferior a su coste.

También separaría:

### Hard gates

Compilación, tipos, políticas de seguridad, invariantes obligatorios, hidden tests críticos, ausencia de regresiones bloqueantes.

### Evidencia de ranking

Mutation score, rendimiento, simplicidad, tamaño del cambio, mantenibilidad, compatibilidad, consumo de recursos.

No utilizaría una suma ponderada ingenua. Primero aplicaría restricciones duras y luego una combinación de Pareto frontier, preferencias lexicográficas y estimación calibrada de riesgo.

Además, el ledger debe conocer **la dependencia entre evidencias**. Diez tests escritos por el mismo modelo a partir de la misma interpretación no constituyen diez observaciones independientes.

---

# 7. Jujutsu: sí como adapter, no como sistema de verdad

Coincido en que `jj` es un muy buen candidato para experimentar:

- working copy como commit;
- conflictos de primera clase;
- auto-rebase;
- operation log;
- compatibilidad con commits Git.

Pero no lo haría el fundamento ontológico de Multiversos.

La documentación oficial deja claro que sólo commits y archivos se almacenan en Git; bookmarks y metadata de nivel superior viven fuera de Git. También advierte que `jj-lib` no es una API estable y que la CLI puede cambiar. El proyecto sigue describiéndose como experimental. 

Por lo tanto:

- Define una interfaz `RepositoryBackend`.
- Implementa inicialmente `GitBackend`.
- Agrega `JjBackend` para aprovechar operation log y first-class conflicts.
- Conserva el ledger de intents, mundos, evidencia y decisiones en un almacén independiente.
- Produce commits Git estándar como resultado portable.
- No dependas de que el operation log local de `jj` sea tu ledger distribuido de provenance.

El operation log permite recuperación de estado, pero no proporciona por sí solo:

- identidad autenticada de agente;
- relación entre prompt y cambio;
- propiedad de hunks tras squash/rebase;
- receipts de oráculos;
- política utilizada para decidir;
- auditoría distribuida;
- firma de la decisión.

También corregiría el caveat de “jj como hobby project”. Esa descripción quedó anticuada: Jujutsu dispone actualmente de gobernanza formal, mantenedores electos y un límite explícito a la influencia de una sola empresa. Sigue siendo experimental, pero ya no es razonable presentarlo simplemente como el hobby de Martin von Zweigbergk. 

Y cambiaría:

> “los agentes nunca se atascan”

por:

> “las operaciones del VCS pueden completarse y persistir un estado conflictivo sin exigir resolución inmediata”.

`jj` permite commitear y seguir transformando conflictos, pero el código puede seguir sin compilar, sin pasar tests o sin ser integrable semánticamente. 

---

# 8. Mergiraf, tree-sitter y almacenamiento semántico

Mergiraf tiene sentido como **resolver opcional**, no como base de corrección. Su propia documentación indica que, cuando el archivo no puede parsearse, vuelve al merge por líneas; incluso con parsing correcto puede dejar conflictos no resueltos para intervención posterior. 

La jerarquía correcta sería:

1. Merge trivial por identidad/hash.
2. Merge textual.
3. Merge estructural con Mergiraf.
4. Reparación generada por agente.
5. Verificación completa por oráculos.
6. Rechazo o escalamiento si la evidencia no es suficiente.

Respecto del content-addressing semántico, no lo llamaría “Fase 4”. Lo trataría como **otro programa de investigación**. Una identidad estable de definición en un repositorio políglota exige manejar:

- resolución de símbolos;
- tipos;
- macros;
- generación de código;
- sobrecargas;
- imports dinámicos;
- preprocesadores;
- DSLs;
- configuración y build graph;
- equivalencia semántica entre refactors.

Un hash de AST obtenido de tree-sitter no resuelve esa identidad. Para Multiversos basta inicialmente con content-addressing de:

- trees Git;
- ambientes;
- inputs;
- traces;
- artefactos;
- evidencia;
- decisiones.

Eso entrega deduplicación, caching y reproducibilidad sin intentar resolver primero la identidad semántica universal del código.

---

# 9. Glean y SCIP

Glean es genuinamente potente: modela definiciones, referencias, call graphs, tipos, imports y otras relaciones como facts tipados consultables mediante Angle. 

Pero no lo pondría en el camino crítico del MVP.

Para la primera versión bastan:

- búsquedas textuales;
- LSP;
- SCIP cuando esté disponible;
- build graph;
- cobertura;
- trazas dinámicas;
- información de archivos tocados.

Glean comienza a ser valioso cuando quieras:

- estimar impacto transitivo;
- invalidar evidencia después de un cambio;
- detectar que dos worlds modifican entidades dependientes;
- encontrar callers para generar pruebas focalizadas;
- responder “qué evidencia quedó obsoleta por este merge”.

Es una extensión natural del **knowledge plane**, no del candidate race inicial.

---

# 10. Provenance: firma no equivale a verdad

in-toto y Sigstore son buenas piezas, pero el documento mezcla a veces tres propiedades diferentes:

1. **Integridad:** nadie alteró el registro.
2. **Identidad:** sabemos quién firmó.
3. **Veracidad:** el contenido firmado corresponde a lo que realmente ocurrió.

Sigstore ayuda principalmente con las dos primeras: una identidad OIDC firma mediante una clave efímera y el evento queda auditable en Rekor. SLSA Build Provenance liga artefactos con una plataforma y una definición de build, pero su modelo sigue requiriendo confiar en el builder que registra la provenance. 

Para Multiversos necesitarías un predicate propio y un trust model explícito:

- ¿La identidad del modelo es sólo un string informado por el cliente?
- ¿El proveedor de inferencia emite un receipt verificable?
- ¿El modelo local está identificado por hash de weights?
- ¿El orquestador puede falsear el trace?
- ¿El runner está dentro de un TEE?
- ¿Quién firma el resultado de los tests?
- ¿Cómo se prueba que el test corrió contra ese tree y ese ambiente?

También evitaría poner prompts, contexto o secretos directamente en un transparency log público. Firmaría digests y conservaría el contenido sensible en un almacén privado y sujeto a controles.

La frase “SLSA Level 2 en una tarde” debe calificarse:

> GitHub Artifact Attestations puede proporcionar Build Level 2 para artefactos construidos bajo sus condiciones, pero eso no equivale a provenance completa de autoría y decisiones de agentes. 

---

# 11. Cómo cambiaría el benchmark

## No basta con SWE-bench Verified

CodeMonkeys valida que la diversidad y la selección aportan valor, pero no valida por sí sola la arquitectura de Multiversos. 

Además, una evaluación empírica posterior encontró que un porcentaje no trivial de parches contados como correctos por SWE-bench Verified no satisfacía realmente el comportamiento esperado; el estudio estimó una inflación de 6,2 puntos porcentuales en las tasas reportadas. 

Por eso utilizaría:

- tests públicos para feedback del agente;
- hidden tests para la decisión experimental;
- differential testing contra el patch humano cuando sea posible;
- generación adversarial de inputs;
- mutation testing;
- pruebas metamórficas y property-based;
- evaluación manual estratificada de una muestra;
- repos y lenguajes distintos de Python.

## Baselines

1. Agente único.
2. Best-of-N con selección aleatoria.
3. Best-of-N por tests públicos.
4. Best-of-N con LLM juez.
5. Test-voting estilo CodeMonkeys.
6. Multiversos con scheduler adaptativo.
7. Oracle ideal retrospectivo, sólo como upper bound.

## Restricción experimental

Todos deben tener el mismo presupuesto total:

\[
B =
\text{tokens}
+
\text{tiempo de runner}
+
\text{coste de oráculos}
+
\text{coste de selección}
\]

Comparar “ocho agentes versus uno” sin igualar coste sólo prueba pass@k, no prueba que el sistema tome mejores decisiones.

## Métricas principales

- true-correct admission rate;
- false-admission rate;
- coste por merge realmente correcto;
- wall-clock hasta una decisión;
- cobertura del conjunto de candidatos;
- regret frente al mejor candidato disponible;
- calibración de confianza;
- cantidad de evidencia desperdiciada;
- regresiones post-integración;
- estabilidad al cambiar de modelo.

El umbral:

> “superar pass@1 con \(N\leq8\)”

es demasiado débil. Casi cualquier generación diversa puede aumentar pass@k.

El criterio correcto sería:

> **A igual presupuesto, superar a best-of-N fijo y test-voting tanto en corrección real como en tasa de falsa admisión, con intervalos de confianza y ablations del scheduler.**

Para la futura línea de composición concurrente, AgenticFlict ofrece un corpus interesante: reporta conflictos textuales en 27,67% de los PRs de agentes que pudieron simularse. Sin embargo, el propio trabajo reconoce que sólo captura conflictos textuales, no incompatibilidades lógicas posteriores al merge. 

---

# 12. Fases revisadas

## Fase A — Candidate race reproducible

- Un intent.
- Un estado base.
- Varias implementaciones alternativas.
- Worktrees o snapshots de contenedor.
- Ledger inmutable de worlds y evidencia.
- Oráculos predefinidos.
- Selección fija.
- Commit Git del ganador.

Objetivo: demostrar que el sistema reproduce y explica cada decisión.

## Fase B — Scheduler adaptativo

- Successive halving.
- Presupuestos por intent.
- Generación condicional de candidatos.
- Mutation testing.
- Adversarial test generation.
- Stopping rules.
- Calibración de riesgo.

Objetivo: superar best-of-N fijo bajo el mismo coste.

## Fase C — Exact-state admission

- Rebase del ganador sobre el trunk actual.
- Invalidación automática de evidencia obsoleta.
- Reejecución mínima necesaria.
- Merge gate sobre el tree exacto que aterrizaría.
- Rollback y audit trail.

Objetivo: separar “candidato correcto en su mundo” de “cambio seguro en el trunk actual”.

## Fase D — Composición de intents

- Recursos y dependencias declarados.
- Detección de overlap.
- SELECT versus COMPOSE.
- Serialización o reparación.
- Speculative integration estilo Zuul.
- Benchmark de tareas concurrentes.

Objetivo: pasar de búsqueda de solución a coordinación de una flota.

## Fase E — Trust y conocimiento

- Attestations.
- Identidades y trust levels.
- SCIP/Glean.
- Impact analysis.
- Queries de lineage.
- Revocación por agente, modelo, política o dependencia comprometida.

Objetivo: gobernanza empresarial y mantenimiento longitudinal.

El almacenamiento semántico por definición quedaría fuera de esta secuencia y en un track de investigación independiente.

---

# 13. Redlines concretos al documento

| Formulación actual | Formulación recomendada |
|---|---|
| “Merges correctos por construcción” | “Composición algebraicamente bien definida y conflictos representables; la corrección del programa requiere oráculos”. |
| “Los agentes nunca se atascan con jj” | “El VCS no bloquea la operación y puede persistir conflictos para resolverlos después”. |
| “Nadie los ha unificado” | “No encontramos, en el corpus y fecha definidos, una integración de estas capacidades concretas”. |
| “jj como hobby project” | “Proyecto comunitario con gobernanza formal, todavía experimental y con APIs inestables”. |
| “SLSA/in-toto resuelve provenance de agentes” | “Proporciona formatos y mecanismos de firma; falta definir el predicate y el trust model del agente”. |
| “Glean está listo para usar como pilar” | “Glean es maduro, pero debe ser un knowledge-plane opcional posterior”. |
| “tree-sitter como fuente semántica” | “tree-sitter es una representación estructural best-effort y debe degradar a texto y compiladores reales”. |
| “CodeCRDT llega hasta ~80% de conflictos complejos” | Eliminar salvo que se cite una tabla y metodología exactas. El abstract reporta 5–10% de conflictos semánticos preliminares junto con convergencia estructural del 100%.  |
| “GitButler deja intacto casi todo el espacio” | “GitButler ya ocupa la capa de ergonomía y versionado para agentes; Multiversos debe diferenciarse en búsqueda, evidencia y admisión”. |

---

# 14. Scope final que utilizaría

> **Multiversos es un plano de control Git-compatible para cambios especulativos de software. Dado un intent versionado, crea mundos candidatos aislados, registra su lineage, asigna dinámicamente presupuesto entre generación y verificación, y acumula evidencia ligada al estado exacto de cada candidato. Su motor de decisión selecciona, rechaza o escala candidatos bajo políticas explícitas y sólo admite a trunk un estado integrado cuya evidencia permanezca fresca. Multiversos no reemplaza Git ni introduce inicialmente un nuevo almacenamiento semántico: versiona posibilidades, evidencia y decisiones antes de producir historia aceptada.**

## Conclusión

**Tu scope actual es correcto como visión norte y como landscape memo, pero demasiado ancho como contribución única.** Yo no seguiría ampliando horizontalmente la investigación. Ya hay suficiente prior art.

El siguiente salto no es encontrar más componentes: es formalizar y demostrar tres cosas:

1. qué es exactamente un `World`;
2. cómo se decide racionalmente dónde gastar el siguiente dólar de verificación;
3. cómo se prueba que la evidencia sigue siendo válida para el estado exacto que se pretende admitir.

Ahí está el producto, el paper y la verdadera barrera técnica.
# go-stdlib-rag

Un sistema **RAG** (Retrieval-Augmented Generation) sobre toda la documentación de la
**standard library de Go**, para consultarla en un chat propio de pregunta-respuesta.
Uso personal, no comercial.

## ¿Qué hace?

1. **Extrae** la documentación de la stdlib directamente desde el código fuente de Go
   (usando `go/doc` y `go/ast` sobre `$GOROOT/src`), generando un `data/stdlib_docs.jsonl`
   con, por cada símbolo documentado: paquete, tipo (`func`/`type`/`const`/`var`/`method`),
   firma completa, comentario de documentación y ejemplos ejecutables (`example_test.go`).
2. **Genera embeddings** de cada fragmento (chunk) utilizando una interfaz agnóstica de `Embedder` (por defecto soportando Jina AI con `jina-embeddings-v3`).
3. **Almacena** los vectores en una base de datos vectorial utilizando una interfaz agnóstica `VectorStore` (por defecto Qdrant Cloud) con metadatos como payload
   (`package`, `symbol_name`, `kind`) para poder filtrar en las consultas.
4. **Consulta**: una CLI recibe una pregunta en lenguaje natural, la vectoriza,
   busca en la base de datos vectorial y devuelve los fragmentos más relevantes con su origen.
5. **Agente**: un REPL conversacional (Gemini + Google ADK) que usa la búsqueda vectorial
   como *tool* para responder preguntas técnicas con código idiomático y validado.

## Stack (costo $0)

| Componente   | Elección                          | Notas                                       |
|--------------|-----------------------------------|---------------------------------------------|
| Lenguaje     | Go 1.25 (proyecto)                | Doc extraída con Go **1.26.5** (última estable) |
| Embeddings   | Agnóstico (defecto: Jina AI)      | Free tier 1M tokens, contexto 8K            |
| Vector DB    | Agnóstico (defecto: Qdrant Cloud) | Free tier (1GB RAM / 4GB disco)             |

## Estructura del proyecto

```
go-stdlib-rag/
├── .gitignore
├── .env.example        # plantilla de credenciales (copiar a .env)
├── go.mod
├── README.md
├── cmd/
│   ├── extract/        # paso 1: extracción stdlib → JSONL
│   ├── ingest/         # paso 3: embeddings + carga a Vector Store
│   ├── query/          # paso 4: CLI de consulta
│   └── agent/          # paso 5: agente conversacional (Gemini + ADK)
├── internal/           # paquetes compartidos
│   ├── chunk/          # lógica de particionado de texto y metadatos
│   └── rag/            # arquitectura agnóstica (patrón Repositorio: interfaces Embedder y VectorStore)
└── data/               # output generado (stdlib_docs.jsonl) — no se versiona
```

## Configuración

```bash
cp .env.example .env
# Editá .env con tus credenciales reales
```

Variables de entorno:

- `EMBEDDER_API_KEY` — API key del proveedor de embeddings (ej. Jina AI).
- `VECTOR_STORE_URL` — endpoint de la base de datos vectorial (ej. clúster de Qdrant Cloud).
- `VECTOR_STORE_API_KEY` — API key de la base de datos vectorial.
- `GEMINI_API_KEY` — API key de Gemini, requerida por `cmd/agent`.

## Uso

```bash
# Consultar (pregunta por argumento o por stdin)
go run ./cmd/query "cómo uso context.WithTimeout"

# Con filtros opcionales por metadata y cantidad de resultados
go run ./cmd/query -k 3 -package context -kind func -has-example "timeout"

# Agente conversacional (REPL), requiere GEMINI_API_KEY
go run ./cmd/agent
```

> **Nota:** `cmd/query` lee `data/stdlib_docs.jsonl` (local, **no versionado** —
> está en `.gitignore`) para mostrar los ejemplos ejecutables en los resultados.
> Si el archivo no existe, `cmd/query` falla con un error claro; regenéralo con
> `go1.26.5 run ./cmd/extract`.

## Estado

🚧 En construcción. Ver el avance por tareas:

- [x] Tarea 1 — Estructura del proyecto, `.gitignore`, `.env.example`, `go.mod`, README.
- [x] Tarea 2 — `cmd/extract`: stdlib (Go 1.26.5) → `data/stdlib_docs.jsonl` (6.246 símbolos / 175 paquetes).
- [x] Tarea 3 — Estrategia de chunking: por símbolo (1 símbolo = 1 chunk = 1 vector).
- [x] Tarea 4 — `cmd/ingest`: embeddings + carga a base de datos vectorial.
- [x] Tarea 5 — `cmd/query`: CLI de consulta (búsqueda top-K, filtros por payload).
- [x] Tarea 6 — Refactor a arquitectura agnóstica (Patrón Repositorio en `internal/rag`).
- [x] Tarea 7 — `cmd/agent`: agente conversacional (Gemini + Google ADK) sobre la tool de búsqueda RAG.

## Licencia / uso

Proyecto personal, no comercial. La documentación de la stdlib de Go es propiedad de
sus autores bajo la licencia BSD de Go.

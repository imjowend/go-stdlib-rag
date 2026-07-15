# go-stdlib-rag

Un sistema **RAG** (Retrieval-Augmented Generation) sobre toda la documentación de la
**standard library de Go**, para consultarla en un chat propio de pregunta-respuesta.
Uso personal, no comercial.

## ¿Qué hace?

1. **Extrae** la documentación de la stdlib directamente desde el código fuente de Go
   (usando `go/doc` y `go/ast` sobre `$GOROOT/src`), generando un `data/stdlib_docs.jsonl`
   con, por cada símbolo documentado: paquete, tipo (`func`/`type`/`const`/`var`/`method`),
   firma completa, doc comment y ejemplos runnable (`example_test.go`).
2. **Genera embeddings** de cada chunk con la API de Jina AI (`jina-embeddings-v3`).
3. **Almacena** los vectores en Qdrant Cloud con metadata como payload
   (`package`, `symbol_name`, `kind`) para poder filtrar en las consultas.
4. **Consulta**: una CLI recibe una pregunta en lenguaje natural, la embebe con Jina,
   busca en Qdrant y devuelve los fragmentos más relevantes con su origen.

## Stack (costo $0)

| Componente   | Elección                          | Notas                                       |
|--------------|-----------------------------------|---------------------------------------------|
| Lenguaje     | Go 1.24 (proyecto)                | Doc extraída con Go **1.26.5** (última estable) |
| Embeddings   | Jina AI (`jina-embeddings-v3`)    | Free tier 1M tokens, contexto 8K            |
| Vector DB    | Qdrant Cloud                      | Free tier (1GB RAM / 4GB disco)             |

## Estructura del proyecto

```
go-stdlib-rag/
├── .gitignore
├── .env.example        # plantilla de credenciales (copiar a .env)
├── go.mod
├── README.md
├── cmd/
│   ├── extract/        # paso 1: extracción stdlib → JSONL
│   ├── ingest/         # paso 3: embeddings + carga a Qdrant
│   └── query/          # paso 4: CLI de consulta
├── internal/           # paquetes compartidos (modelos, clientes Jina/Qdrant)
└── data/               # output generado (stdlib_docs.jsonl) — no se versiona
```

## Configuración

```bash
cp .env.example .env
# Editá .env con tus credenciales reales de Jina y Qdrant
```

Variables de entorno:

- `JINA_API_KEY` — API key del free tier de Jina AI.
- `QDRANT_URL` — endpoint del cluster de Qdrant Cloud.
- `QDRANT_API_KEY` — API key del cluster de Qdrant Cloud.

## Uso

```bash
# Consultar (pregunta por argumento o por stdin)
go run ./cmd/query "cómo uso context.WithTimeout"

# Con filtros opcionales por metadata y cantidad de resultados
go run ./cmd/query -k 3 -package context -kind func -has-example "timeout"
```

> **Nota:** `cmd/query` lee `data/stdlib_docs.jsonl` (local, **no versionado** —
> está en `.gitignore`) para mostrar los ejemplos runnable en los resultados.
> Si el archivo no existe, `cmd/query` falla con un error claro; regeneralo con
> `go1.26.5 run ./cmd/extract`.

## Estado

🚧 En construcción. Ver el avance por tareas:

- [x] Tarea 1 — Estructura del proyecto, `.gitignore`, `.env.example`, `go.mod`, README.
- [x] Tarea 2 — `cmd/extract`: stdlib (Go 1.26.5) → `data/stdlib_docs.jsonl` (6.246 símbolos / 175 paquetes).
- [x] Tarea 3 — Estrategia de chunking: por símbolo (1 símbolo = 1 chunk = 1 vector).
- [x] Tarea 4 — `cmd/ingest`: embeddings (Jina v3) + carga a Qdrant (6.246 puntos, validado con ingesta real).
- [x] Tarea 5 — `cmd/query`: CLI de consulta (embed query con Jina, búsqueda top-K en Qdrant, filtros por payload).

## Licencia / uso

Proyecto personal, no comercial. La documentación de la stdlib de Go es propiedad de
sus autores bajo la licencia BSD de Go.

# Arquitectura: Go Stdlib RAG

Este documento describe el diseño, flujo de datos y decisiones clave de arquitectura del proyecto `go-stdlib-rag`.

## Diagrama de Flujo

El sistema se compone de un pipeline de cuatro etapas (Extracción → Chunking → Ingestión → Consulta), donde cada componente hace una sola cosa bien, minimizando el acoplamiento:

```mermaid
flowchart TD
    %% Componentes
    GoStdLib[("Go Standard Library\n(src/)")]
    Extract["cmd/extract\n(go/doc + go/ast)"]
    JSONL[("data/stdlib_docs.jsonl")]
    Chunk["internal/chunk\n(Chunking & Payload)"]
    Ingest["cmd/ingest"]
    EmbedderAPI["API de Embeddings\n(ej. Jina AI)"]
    VectorDB[("Vector Store\n(ej. Qdrant)")]
    Query["cmd/query"]
    Terminal[("Terminal / Usuario")]

    %% Flujo
    GoStdLib -->|Lectura AST/Docs| Extract
    Extract -->|Escritura JSON Lines| JSONL
    JSONL -->|Lectura Secuencial| Ingest
    Ingest -->|Formateo de texto| Chunk
    Chunk -->|Texto final| Ingest
    Ingest <-->|Batch Embeddings| EmbedderAPI
    Ingest -->|Upsert Puntos| VectorDB
    Terminal -->|Pregunta en Lenguaje Natural| Query
    Query <-->|Embed Query| EmbedderAPI
    Query <-->|Cosine Search + Filters| VectorDB
    JSONL -.->|Enriquecer Ejemplos| Query
    Query -->|Resultados| Terminal
```

## Flujo de Datos Completo

1. **Extract (`cmd/extract`)**: Navega recursivamente por el árbol fuente local de la biblioteca estándar de Go (`$GOROOT/src`). Utiliza `go/doc`, `go/ast` y `go/parser` para encontrar, parsear y extraer símbolos documentados y exportados, junto con sus ejemplos ejecutables. Serializa el resultado en formato JSONL (`data/stdlib_docs.jsonl`).
2. **Chunk (`internal/chunk`)**: Es una capa interna puramente funcional que recibe la representación abstracta de un símbolo (`docmodel.Symbol`) y la transforma en el texto crudo que será incrustado por el modelo. También genera el ID determinista y el payload de metadatos.
3. **Ingest (`cmd/ingest`)**: Lee el JSONL. Por cada símbolo, pide a `chunk` el texto y el ID. Empaqueta los textos en lotes, llama a la interfaz `Embedder` para obtener los vectores, y hace un *upsert* utilizando la interfaz `VectorStore` adjuntando el payload de metadatos.
4. **Query (`cmd/query`)**: Toma una pregunta del usuario, la vectoriza usando la interfaz `Embedder` (usando el adaptador de consulta asimétrica si el proveedor lo soporta), y busca en el `VectorStore` los $K$ vectores más cercanos. Finalmente, recupera ejemplos completos del JSONL local para mostrar una respuesta detallada al usuario.

## Decisiones Clave de Arquitectura

### 1. Arquitectura Agnóstica (Patrón Repositorio)
Para garantizar la flexibilidad y evitar el acoplamiento fuerte con proveedores específicos, el sistema utiliza un diseño basado en el patrón Repositorio dentro del paquete `internal/rag`.
* **Interfaces Clave:** Se definen las interfaces `Embedder` (para la generación de vectores) y `VectorStore` (para el almacenamiento y búsqueda).
* **Ventaja:** Permite intercambiar fácilmente el proveedor de embeddings o la base de datos vectorial sin modificar la lógica de negocio en `cmd/ingest` o `cmd/query`.

### 2. Chunking por Símbolo vs. por Paquete
Decidimos que la unidad atómica de información en la base de datos vectorial sea el **Símbolo** (una función, tipo, constante o variable individual) y no un paquete o archivo entero.
* **Por qué:** Los paquetes en la biblioteca estándar pueden ser masivos (ej. `net/http` o `syscall`). Al hacer particionado por símbolo, los vectores capturan el significado semántico altamente específico de esa pieza de código. Durante una consulta, el sistema recupera la función exacta necesaria sin forzar al modelo (o al usuario) a filtrar ruido contextual irrelevante.
* **Estructura del Chunk:** Cada documento de texto contiene primero la ruta del paquete y la firma de la función (para anclar semánticamente), seguido por su comentario docstring y finalmente cualquier ejemplo de código asociado.

### 3. Implementaciones por Defecto: Jina AI + Qdrant Cloud
El sistema provee implementaciones oficiales para las interfaces mencionadas utilizando:
* **Jina AI (`jina-embeddings-v3`)**: Este modelo de embeddings de última generación soporta un contexto muy largo (8192 tokens) y adaptadores LoRA específicos para la tarea. Usamos el tipo `retrieval.passage` para la indexación y `retrieval.query` para la búsqueda, logrando una "retrieval asimétrico" altamente efectivo para código y lenguaje natural.
* **Qdrant Cloud**: Base de datos vectorial eficiente, moderna y basada en Rust. Qdrant soporta potentes índices de *payload* que permiten filtrar por paquete o tipo durante la búsqueda antes de calcular la distancia coseno.

### 4. Idempotencia vía UUID Determinista (UUIDv5)
La ingestión fue diseñada para ser **completamente idempotente**. En lugar de pedirle a la base de datos vectorial que autogenere un UUID al insertar, generamos nosotros un UUID en `internal/chunk` de manera determinista utilizando SHA-1 y un *namespace* propio sobre una llave única del símbolo (`Paquete|Kind|Receptor|Nombre`).
* **Ventaja:** Si se interrumpe la ingestión o corremos una nueva extracción en una versión más nueva de Go, volver a correr `cmd/ingest` reemplazará transparentemente los vectores actualizados (*upsert*) usando el mismo ID, garantizando que **no habrá datos duplicados** ni requeriremos lógica de borrado y recreación del índice completo.

### 5. Trade-off: Truncamiento del Outlier (ej. `syscall`)
Implementamos un techo defensivo de tokens (`MaxDocTokens = 6000`), calculado conservadoramente, para asegurar que ningún *chunk* exceda el contexto de los modelos de embeddings.
* **El Problema:** La inmensa mayoría de los símbolos en Go son cortos, pero algunos paquetes exponen bloques gigantes de declaración masiva. Por ejemplo, las constantes de `syscall` se agrupan bajo un único nodo AST.
* **La Decisión:** En caso de superar el límite, la función `chunk.BuildText` trunca **únicamente el bloque de firmas (código base)** desde el final, pero preserva siempre el *doc comment*.
* **Por qué:** Es un trade-off intencional. La alternativa era romper artificialmente las declaraciones de variables/constantes y destruir la cohesión del símbolo. Preferimos tener un *chunk* truncado que conserva su documentación base para ese caso atípico en `syscall`, manteniendo la simplicidad del pipeline y asegurando la robustez para el 99.9% de los símbolos normales.

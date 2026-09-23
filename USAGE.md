# Guía de Uso y Troubleshooting

Esta guía detalla los requisitos, los pasos para ejecutar el pipeline completo de `go-stdlib-rag` y las soluciones a los problemas más comunes.

## Requisitos Previos

1. **Go 1.26.5 (Aislado)**: Necesario específicamente para el paso de extracción (`cmd/extract`). Usar la versión exacta garantiza que los paquetes de la biblioteca estándar (y sus AST/Docs) extraídos coincidan perfectamente con la versión deseada.
   - Instalación: `go install golang.org/dl/go1.26.5@latest && go1.26.5 download`
2. **Go (Default)**: Cualquier versión moderna de Go (1.21+) para compilar y ejecutar los binarios de ingestión y consulta (`cmd/ingest` y `cmd/query`).
3. **API Keys y Credenciales**:
   - `EMBEDDER_API_KEY`: Para generar embeddings (por defecto usa Jina AI).
   - `VECTOR_STORE_URL`: La URL de tu clúster de base de datos vectorial (por defecto Qdrant Cloud).
   - `VECTOR_STORE_API_KEY`: La clave de API de tu base de datos vectorial.

Copia el archivo `.env.example` a `.env` y rellena las variables:
```bash
cp .env.example .env
```
*(Nota: El archivo `.env` está ignorado en git por seguridad).*

---

## Cómo Ejecutar (Paso a Paso)

El pipeline está diseñado para ejecutarse en un orden específico.

### Paso 1: Extracción
Extrae los símbolos documentados de la biblioteca estándar local y los guarda en un archivo JSONL.
**Importante:** Debes usar la versión de Go que deseas documentar.
```bash
go1.26.5 run ./cmd/extract
```
*Resultado esperado:* Se creará el archivo `data/stdlib_docs.jsonl`.

### Paso 2: Ingestión
Lee el archivo JSONL, genera los embeddings utilizando el proveedor configurado (ej. Jina AI) y los guarda en el Vector Store (ej. Qdrant).
```bash
# Opcional: Puedes hacer un dry-run para verificar cuántos tokens se usarán (útil con el proveedor por defecto Jina AI).
go run ./cmd/ingest -dry-run

# Ejecución real:
go run ./cmd/ingest
```
*Nota:* El comando respeta automáticamente los límites de cuota (TPM/RPM) en caso de usar el tier gratuito de Jina AI.

### Paso 3: Consulta
Realiza preguntas en lenguaje natural sobre la biblioteca estándar.
```bash
# Por argumento:
go run ./cmd/query "how do I use context.WithTimeout"

# O por stdin:
echo "how do I parse a JSON file" | go run ./cmd/query
```

También puedes filtrar por paquete o tipo:
```bash
go run ./cmd/query -package net/http -kind func "how to start a server"
```

---

## Troubleshooting (Errores Comunes)

### 1. Error: `missing required env vars...`
**Síntoma:** Al correr `ingest` o `query`, el programa falla indicando que faltan variables de entorno.
**Solución:** 
- Asegúrate de haber creado el archivo `.env` en la raíz del proyecto.
- Si lo creaste con otro nombre o en otra ruta, pásalo usando el flag `-env`:
  `go run ./cmd/query -env /ruta/a/mi/.env "pregunta"`

### 2. Error: `data/stdlib_docs.jsonl not found` (en cmd/query o cmd/ingest)
**Síntoma:** `cmd/query` dice que no encuentra el archivo JSONL y pide correr `cmd/extract` primero.
**Solución:**
- El archivo `data/stdlib_docs.jsonl` es generado localmente y está ignorado por git.
- **Debes ejecutar el Paso 1 (`cmd/extract`) al menos una vez** en tu máquina antes de intentar ingerir o consultar. El comando `query` lo necesita localmente para poder mostrar los ejemplos de código completos.

### 3. Error: `source tree not found at .../src` (en cmd/extract)
**Síntoma:** El comando de extracción no encuentra el código fuente de Go.
**Solución:**
- Verifica que el binario de Go que estás usando (ej. `go1.26.5`) esté correctamente descargado y tenga su árbol `src/`. Correr `go1.26.5 download` suele solucionarlo si lo instalaste vía `golang.org/dl`.

### 4. Lentitud extrema durante `cmd/ingest`
**Síntoma:** La ingestión avanza muy lento o se pausa.
**Solución:**
- Esto puede ser **esperado y por diseño**. Si usas el proveedor Jina AI gratuito, el limitador de tasa interno retrasa las peticiones para asegurar que no excedas el límite de 100,000 TPM (Tokens Por Minuto). Déjalo correr en segundo plano; eventualmente terminará de forma segura.

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
	"github.com/imjowend/go-stdlib-rag/internal/rag"
)

// SearchDocsArgs define los parámetros de entrada (argumentos) esperados por la herramienta de búsqueda RAG.
type SearchDocsArgs struct {
	// Concepto o duda técnica de Go a buscar
	Query   string `json:"query"`
	// Filtro opcional por paquete de la stdlib (ej: 'net/http', 'context')
	Package string `json:"package,omitempty"`
	// Filtro opcional por tipo de símbolo ('func', 'type', 'const', 'var')
	Kind    string `json:"kind,omitempty"`
}

// RAGTool encapsula la lógica de búsqueda integrando el motor de embeddings y la base de datos vectorial.
type RAGTool struct {
	Collection string
	Embedder   rag.Embedder
	Store      rag.VectorStore
	Examples   map[string][]docmodel.Example
}

// NewRAGTool inicializa y devuelve una nueva instancia de la herramienta RAGTool.
func NewRAGTool(collection string, embedder rag.Embedder, store rag.VectorStore, examples map[string][]docmodel.Example) *RAGTool {
	return &RAGTool{
		Collection: collection,
		Embedder:   embedder,
		Store:      store,
		Examples:   examples,
	}
}

// Search es la función manejadora de la herramienta. Es invocada por el LLM cuando decide buscar en la base de datos.
// Se encarga de incrustar la consulta (query), construir el filtro e interrogar al motor vectorial.
func (t *RAGTool) Search(ctx context.Context, args SearchDocsArgs) (string, error) {
	fmt.Printf("\n[Tool: rag_search] Buscando en VectorStore: %q (pkg: %q, kind: %q)\n", args.Query, args.Package, args.Kind)

	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	// Generar embeddings para la consulta (query)
	vectors, _, err := t.Embedder.Embed(ctx, "retrieval.query", []string{args.Query})
	if err != nil {
		return "", fmt.Errorf("embedding query: %w", err)
	}

	// Construir los filtros de búsqueda (opcionales)
	var must []map[string]any
	if args.Package != "" {
		must = append(must, map[string]any{"key": "package", "match": map[string]any{"value": args.Package}})
	}
	if args.Kind != "" {
		must = append(must, map[string]any{"key": "kind", "match": map[string]any{"value": args.Kind}})
	}

	var filter map[string]any
	if len(must) > 0 {
		filter = map[string]any{"must": must}
	}

	// Buscar en la Base de Datos Vectorial
	results, err := t.Store.Search(ctx, t.Collection, vectors[0], 5, filter)
	if err != nil {
		return "", fmt.Errorf("searching qdrant: %w", err)
	}

	if len(results) == 0 {
		return "No matches found.", nil
	}

	// Formatear los resultados en texto legible para el LLM
	var out strings.Builder
	for i, r := range results {
		pkg, _ := r.Payload["package"].(string)
		name, _ := r.Payload["symbol_name"].(string)
		kind, _ := r.Payload["kind"].(string)
		recv, _ := r.Payload["recv"].(string)
		sig, _ := r.Payload["signature"].(string)
		doc, _ := r.Payload["doc"].(string)

		label := kind
		if recv != "" {
			label += " (" + recv + ")"
		}

		fmt.Fprintf(&out, "[%d] %s.%s (Kind: %s, Score: %.3f)\n", i+1, pkg, name, label, r.Score)
		if sig != "" {
			fmt.Fprintf(&out, "Signature:\n  %s\n", strings.ReplaceAll(sig, "\n", "\n  "))
		}
		if doc != "" {
			fmt.Fprintf(&out, "Documentation:\n  %s\n", strings.ReplaceAll(doc, "\n", "\n  "))
		}
		
		for _, ex := range t.Examples[r.ID] {
			exName := "Example"
			if ex.Name != "" {
				exName += " " + ex.Name
			}
			fmt.Fprintf(&out, "\n%s:\n```go\n%s\n```\n", exName, ex.Code)
			if ex.Output != "" {
				fmt.Fprintf(&out, "Output:\n```\n%s\n```\n", ex.Output)
			}
		}
		out.WriteString("\n---\n")
	}

	return out.String(), nil
}

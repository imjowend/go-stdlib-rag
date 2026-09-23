// Comando query responde a una pregunta en lenguaje natural sobre la librería
// estándar de Go. Utiliza Jina para convertir la pregunta en un vector
// (tarea=retrieval.query), busca en la colección go_stdlib_docs en Qdrant e
// imprime los símbolos más relevantes con sus metadatos (paquete, nombre del
// símbolo, tipo, tiene ejemplo) y contenido (firma, documentación y ejemplo
// ejecutable si existe).
//
// La pregunta se lee de los argumentos de la línea de comandos o, si no se
// proporcionan, desde la entrada estándar (stdin):
//
//	go run ./cmd/query "how do I use context.WithTimeout"
//	echo "how do I use context.WithTimeout" | go run ./cmd/query
//
// Los ejemplos se muestran leyendo el archivo local data/stdlib_docs.jsonl (el cual
// está ignorado por git); regenerarlo con `go1.26.5 run ./cmd/extract` si falta.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/imjowend/go-stdlib-rag/internal/chunk"
	"github.com/imjowend/go-stdlib-rag/internal/config"
	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
	"github.com/imjowend/go-stdlib-rag/internal/jina"
	"github.com/imjowend/go-stdlib-rag/internal/qdrant"
	"github.com/imjowend/go-stdlib-rag/internal/rag"
)

// maxExampleLines limita cuántas líneas de un ejemplo (código o salida) imprimimos,
// para que un ejemplo muy largo no sature la terminal.
const maxExampleLines = 40

func main() {
	k := flag.Int("k", 5, "number of top results to return")
	pkg := flag.String("package", "", "filter: only symbols in this package (e.g. net/http)")
	kind := flag.String("kind", "", "filter: only this kind (func|method|type|const|var)")
	hasExample := flag.Bool("has-example", false, "filter: only symbols that have a runnable example")
	asJSON := flag.Bool("json", false, "output raw JSON instead of the human-readable format")
	envPath := flag.String("env", ".env", "path to .env file")
	jsonlPath := flag.String("jsonl", "data/stdlib_docs.jsonl", "path to the extracted JSONL (for showing examples)")
	flag.Parse()

	// Pregunta desde los argumentos, o desde stdin si no se dieron argumentos.
	question := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if question == "" {
		b, _ := io.ReadAll(os.Stdin)
		question = strings.TrimSpace(string(b))
	}
	if question == "" {
		log.Fatalf("no question provided: pass it as arguments or via stdin")
	}

	// Validación temprana y explícita: cmd/query depende del JSONL para mostrar
	// ejemplos, pero está ignorado por git (salida generada). Fallar claramente.
	if _, err := os.Stat(*jsonlPath); err != nil {
		log.Fatalf("%s not found: run cmd/extract first (e.g. `go1.26.5 run ./cmd/extract`)", *jsonlPath)
	}
	examples, err := loadExamples(*jsonlPath)
	if err != nil {
		log.Fatalf("loading examples from %s: %v", *jsonlPath, err)
	}

	cfg, err := config.Load(*envPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Crear un contexto interceptando señales del sistema operativo (Graceful Shutdown).
	// Permite cancelar peticiones bloqueantes o muy largas pulsando Ctrl+C.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var embedder rag.Embedder = jina.New(cfg.EmbedderAPIKey)
	var store rag.VectorStore = qdrant.New(cfg.VectorStoreURL, cfg.VectorStoreAPIKey)

	// Construir el filtro opcional de payload y asegurarse de que sus índices existan
	// (únicamente si se solicitó un filtro explícito; de otro modo no hay llamadas extra).
	filter := buildFilter(*pkg, *kind, *hasExample)
	if filter != nil {
		fields := map[string]string{}
		if *pkg != "" {
			fields["package"] = "keyword"
		}
		if *kind != "" {
			fields["kind"] = "keyword"
		}
		if *hasExample {
			fields["has_example"] = "bool"
		}
		qc, ok := store.(*qdrant.Client)
		if ok {
			created, err := qc.EnsurePayloadIndex(ctx, cfg.Collection, fields)
			if err != nil {
				log.Fatalf("ensuring payload indexes: %v", err)
			}
			if len(created) > 0 {
				log.Printf("created payload indexes: %s", strings.Join(created, ", "))
			}
		}
	}

	// Generar el embedding (vector) para la consulta usando la tarea específica "retrieval.query".
	vectors, _, err := embedder.Embed(ctx, jina.TaskQuery, []string{question})
	if err != nil {
		log.Fatalf("embedding query: %v", err)
	}

	// Buscar en la base de datos (con el filtro opcional).
	results, err := store.Search(ctx, cfg.Collection, vectors[0], *k, filter)
	if err != nil {
		log.Fatalf("searching: %v", err)
	}

	if *asJSON {
		printJSON(results, examples)
		return
	}
	printResults(question, results, examples)
}

// loadExamples lee el archivo JSONL e indexa los ejemplos de cada símbolo usando
// el mismo ID de punto determinista usado en el momento de la ingesta.
func loadExamples(path string) (map[string][]docmodel.Example, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	out := make(map[string][]docmodel.Example)
	for sc.Scan() {
		var s docmodel.Symbol
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return nil, err
		}
		if len(s.Examples) > 0 {
			out[chunk.PointID(s)] = s.Examples
		}
	}
	return out, sc.Err()
}

// buildFilter ensambla un filtro "must" de Qdrant a partir de las flags solicitadas,
// o devuelve nil si no se solicitó ningún filtro.
func buildFilter(pkg, kind string, hasExample bool) map[string]any {
	var must []map[string]any
	if pkg != "" {
		must = append(must, map[string]any{"key": "package", "match": map[string]any{"value": pkg}})
	}
	if kind != "" {
		must = append(must, map[string]any{"key": "kind", "match": map[string]any{"value": kind}})
	}
	if hasExample {
		must = append(must, map[string]any{"key": "has_example", "match": map[string]any{"value": true}})
	}
	if len(must) == 0 {
		return nil
	}
	return map[string]any{"must": must}
}

// printResults imprime los resultados de la búsqueda en stdout en un formato legible para humanos.
func printResults(question string, results []rag.SearchResult, examples map[string][]docmodel.Example) {
	fmt.Printf("Query: %s\n%d results\n\n", question, len(results))
	if len(results) == 0 {
		fmt.Println("(no matches)")
		return
	}
	const sep = "────────────────────────────────────────────────────────────"
	for i, r := range results {
		pkg := str(r.Payload, "package")
		name := str(r.Payload, "symbol_name")
		kind := str(r.Payload, "kind")
		recv := str(r.Payload, "recv")
		sig := str(r.Payload, "signature")
		doc := str(r.Payload, "doc")
		hasEx := boolv(r.Payload, "has_example")

		exMark := ""
		if hasEx {
			exMark = "  ·  ✎ example"
		}
		fmt.Printf("[%d] %s.%s  ·  %s  ·  score %.3f%s\n", i+1, pkg, name, kindLabel(kind, recv), r.Score, exMark)
		if sig != "" {
			fmt.Printf("\n%s\n", indent(sig, "    "))
		}
		if doc != "" {
			fmt.Printf("\n%s\n", indent(doc, "    "))
		}
		for _, ex := range examples[r.ID] {
			label := "Example"
			if ex.Name != "" {
				label += " " + ex.Name
			}
			fmt.Printf("\n    %s:\n%s\n", label, indent(capLines(ex.Code, maxExampleLines), "        "))
			if ex.Output != "" {
				fmt.Printf("    Output:\n%s\n", indent(capLines(ex.Output, maxExampleLines), "        "))
			}
		}
		fmt.Printf("\n%s\n\n", sep)
	}
}

// printJSON outputs the search results to stdout as a JSON array.
func printJSON(results []rag.SearchResult, examples map[string][]docmodel.Example) {
	type hit struct {
		ID       string             `json:"id"`
		Score    float32            `json:"score"`
		Payload  map[string]any     `json:"payload"`
		Examples []docmodel.Example `json:"examples,omitempty"`
	}
	arr := make([]hit, 0, len(results))
	for _, r := range results {
		arr = append(arr, hit{ID: r.ID, Score: r.Score, Payload: r.Payload, Examples: examples[r.ID]})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(arr); err != nil {
		log.Fatalf("encoding json: %v", err)
	}
}

// kindLabel renders the kind, adding the receiver type for methods.
func kindLabel(kind, recv string) string {
	if recv != "" {
		return kind + " (" + recv + ")"
	}
	return kind
}

// indent prefixes every non-empty line of s with pad.
func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n")
}

// capLines keeps at most max lines of s, adding a marker if it was truncated.
func capLines(s string, max int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n") + "\n... (truncated)"
}

// str extracts a string value from the metadata map.
func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// boolv extracts a boolean value from the metadata map.
func boolv(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

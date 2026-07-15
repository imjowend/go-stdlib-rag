// Command query answers a natural-language question about the Go standard
// library. It embeds the question with Jina (task=retrieval.query), searches
// the go_stdlib_docs collection in Qdrant, and prints the most relevant
// symbols with their metadata (package, symbol_name, kind, has_example) and
// content (signature, doc, and runnable example if any).
//
// The question is read from the command-line arguments or, if none are given,
// from stdin:
//
//	go run ./cmd/query "how do I use context.WithTimeout"
//	echo "how do I use context.WithTimeout" | go run ./cmd/query
//
// Examples are shown by reading the local data/stdlib_docs.jsonl (which is
// gitignored); regenerate it with `go1.26.5 run ./cmd/extract` if missing.
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
	"strings"

	"github.com/imjowend/go-stdlib-rag/internal/chunk"
	"github.com/imjowend/go-stdlib-rag/internal/config"
	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
	"github.com/imjowend/go-stdlib-rag/internal/jina"
	"github.com/imjowend/go-stdlib-rag/internal/qdrant"
)

// maxExampleLines caps how many lines of an example (code or output) we print,
// so a huge example does not flood the terminal.
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

	// Question from args, or from stdin if no args were given.
	question := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if question == "" {
		b, _ := io.ReadAll(os.Stdin)
		question = strings.TrimSpace(string(b))
	}
	if question == "" {
		log.Fatalf("no question provided: pass it as arguments or via stdin")
	}

	// Early, explicit validation: cmd/query depends on the JSONL to show
	// examples, but it is gitignored (generated output). Fail clearly.
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

	ctx := context.Background()
	jc := jina.New(cfg.JinaAPIKey)
	qc := qdrant.New(cfg.QdrantURL, cfg.QdrantAPIKey)

	// Build the optional payload filter and ensure its indexes exist (only
	// when a filter is actually requested; otherwise no extra Qdrant call).
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
		created, err := qc.EnsurePayloadIndex(ctx, cfg.Collection, fields)
		if err != nil {
			log.Fatalf("ensuring payload indexes: %v", err)
		}
		if len(created) > 0 {
			log.Printf("created payload indexes: %s", strings.Join(created, ", "))
		}
	}

	// Embed the query using the asymmetric retrieval query adapter.
	vectors, _, err := jc.Embed(ctx, jina.TaskQuery, []string{question})
	if err != nil {
		log.Fatalf("embedding query: %v", err)
	}

	results, err := qc.Search(ctx, cfg.Collection, vectors[0], *k, filter)
	if err != nil {
		log.Fatalf("searching: %v", err)
	}

	if *asJSON {
		printJSON(results, examples)
		return
	}
	printResults(question, results, examples)
}

// loadExamples reads the JSONL and indexes each symbol's examples by the same
// deterministic point ID used at ingest time, so results can be enriched.
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

// buildFilter assembles a Qdrant "must" filter from the requested flags, or
// returns nil if no filter was requested.
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

func printResults(question string, results []qdrant.SearchResult, examples map[string][]docmodel.Example) {
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

func printJSON(results []qdrant.SearchResult, examples map[string][]docmodel.Example) {
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

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func boolv(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

// Command ingest reads data/stdlib_docs.jsonl, builds one embedding document
// per symbol, embeds them via the Jina API (jina-embeddings-v3) and upserts
// them into a Qdrant Cloud collection with metadata payload for filtering.
//
// Use -dry-run to validate the whole pipeline offline (no Jina/Qdrant calls,
// no token spend) before running the real ingestion.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"

	"github.com/imjowend/go-stdlib-rag/internal/chunk"
	"github.com/imjowend/go-stdlib-rag/internal/config"
	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
	"github.com/imjowend/go-stdlib-rag/internal/jina"
	"github.com/imjowend/go-stdlib-rag/internal/qdrant"
)

func main() {
	in := flag.String("in", "data/stdlib_docs.jsonl", "input JSONL path")
	envPath := flag.String("env", ".env", "path to .env file")
	batchSize := flag.Int("batch", 32, "symbols per Jina request")
	maxReqTokens := flag.Int("req-tokens", 7000, "max estimated tokens per Jina request")
	tpm := flag.Int("tpm", 90000, "target tokens-per-minute ceiling (safety margin under Jina's 100K)")
	rpm := flag.Int("rpm", 90, "target requests-per-minute ceiling (safety margin under Jina's 100)")
	limit := flag.Int("limit", 0, "process only the first N symbols (0 = all)")
	dryRun := flag.Bool("dry-run", false, "validate pipeline offline: no Jina/Qdrant calls, no token spend")
	flag.Parse()

	symbols, err := readSymbols(*in, *limit)
	if err != nil {
		log.Fatalf("reading %s: %v", *in, err)
	}
	log.Printf("loaded %d symbols from %s", len(symbols), *in)

	// Build documents, payloads and IDs up front (pure, no network).
	type item struct {
		text    string
		payload map[string]any
		id      string
		tokens  int
	}
	items := make([]item, len(symbols))
	var truncated, totalTokens int
	for i, s := range symbols {
		text, wasTrunc := chunk.BuildText(s)
		if wasTrunc {
			truncated++
		}
		t := chunk.EstimateTokens(text)
		totalTokens += t
		items[i] = item{text: text, payload: chunk.Payload(s), id: chunk.PointID(s), tokens: t}
	}
	log.Printf("built %d chunks | ~%d est. tokens total | %d truncated by cap", len(items), totalTokens, truncated)

	if *dryRun {
		log.Printf("[dry-run] sample chunk (%s.%s):\n%s", symbols[0].Package, symbols[0].Name, items[0].text)
		log.Printf("[dry-run] est. tokens=%d, id=%s", items[0].tokens, items[0].id)
		log.Printf("[dry-run] would create/ensure Qdrant collection with dim=%d (Cosine)", jina.Dim)
		log.Printf("[dry-run] OK: no Jina/Qdrant calls made, no tokens spent")
		return
	}

	cfg, err := config.Load(*envPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx := context.Background()
	jc := jina.New(cfg.JinaAPIKey)
	qc := qdrant.New(cfg.QdrantURL, cfg.QdrantAPIKey)

	created, err := qc.EnsureCollection(ctx, cfg.Collection, jina.Dim)
	if err != nil {
		log.Fatalf("ensuring collection %q: %v", cfg.Collection, err)
	}
	if created {
		log.Printf("created Qdrant collection %q (dim=%d, Cosine)", cfg.Collection, jina.Dim)
	} else {
		log.Printf("Qdrant collection %q already exists", cfg.Collection)
	}

	lim := newLimiter(*tpm, *rpm)
	var done, spentTokens int

	for start := 0; start < len(items); {
		// Assemble one batch honoring both the count and per-request token cap.
		end := start
		batchTokens := 0
		for end < len(items) && end-start < *batchSize {
			nt := items[end].tokens
			if end > start && batchTokens+nt > *maxReqTokens {
				break
			}
			batchTokens += nt
			end++
		}

		texts := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			texts = append(texts, items[i].text)
		}

		lim.wait(batchTokens) // pace to stay under TPM/RPM

		vectors, used, err := jc.Embed(ctx, jina.TaskPassage, texts)
		if err != nil {
			log.Fatalf("embedding batch [%d:%d]: %v", start, end, err)
		}
		spentTokens += used

		points := make([]qdrant.Point, 0, len(vectors))
		for i, v := range vectors {
			it := items[start+i]
			points = append(points, qdrant.Point{ID: it.id, Vector: v, Payload: it.payload})
		}
		if err := qc.Upsert(ctx, cfg.Collection, points); err != nil {
			log.Fatalf("upserting batch [%d:%d]: %v", start, end, err)
		}

		done += end - start
		log.Printf("progress: %d/%d symbols upserted (jina tokens so far: %d)", done, len(items), spentTokens)
		start = end
	}

	log.Printf("done: %d symbols embedded and upserted into %q | jina tokens billed: %d", done, cfg.Collection, spentTokens)
}

func readSymbols(path string, limit int) ([]docmodel.Symbol, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	var out []docmodel.Symbol
	for sc.Scan() {
		var s docmodel.Symbol
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return nil, err
		}
		out = append(out, s)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, sc.Err()
}

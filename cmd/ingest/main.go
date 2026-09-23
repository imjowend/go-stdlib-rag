// Comando ingest procesa un archivo data/stdlib_docs.jsonl, construye un documento de
// embedding por cada símbolo, los vectoriza vía la API de Jina (jina-embeddings-v3)
// y los inserta en una colección de Qdrant Cloud con metadatos para filtrado.
//
// Use -dry-run para validar toda la tubería offline (sin llamadas a Jina/Qdrant,
// sin consumo de tokens) antes de ejecutar la ingesta real.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/imjowend/go-stdlib-rag/internal/chunk"
	"github.com/imjowend/go-stdlib-rag/internal/config"
	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
	"github.com/imjowend/go-stdlib-rag/internal/jina"
	"github.com/imjowend/go-stdlib-rag/internal/qdrant"
	"github.com/imjowend/go-stdlib-rag/internal/rag"
)

func main() {
	in := flag.String("in", "data/stdlib_docs.jsonl", "ruta del archivo JSONL de entrada")
	envPath := flag.String("env", ".env", "ruta al archivo .env")
	batchSize := flag.Int("batch", 32, "símbolos por solicitud a Jina")
	maxReqTokens := flag.Int("req-tokens", 7000, "máximo estimado de tokens por solicitud a Jina")
	tpm := flag.Int("tpm", 90000, "límite objetivo de tokens por minuto (margen de seguridad bajo los 100K de Jina)")
	rpm := flag.Int("rpm", 90, "límite objetivo de solicitudes por minuto (margen de seguridad bajo las 100 de Jina)")
	limit := flag.Int("limit", 0, "procesar solo los primeros N símbolos (0 = todos)")
	dryRun := flag.Bool("dry-run", false, "validar tubería offline: sin llamadas a Jina/Qdrant, sin consumo de tokens")
	flag.Parse()

	symbols, err := readSymbols(*in, *limit)
	if err != nil {
		log.Fatalf("leyendo %s: %v", *in, err)
	}
	log.Printf("cargados %d símbolos desde %s", len(symbols), *in)

	// Construir documentos, payloads y IDs por adelantado (puro, sin red).
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
	log.Printf("construidos %d bloques | ~%d tokens est. totales | %d truncados por límite", len(items), totalTokens, truncated)

	if *dryRun {
		log.Printf("[dry-run] bloque de muestra (%s.%s):\n%s", symbols[0].Package, symbols[0].Name, items[0].text)
		log.Printf("[dry-run] tokens est.=%d, id=%s", items[0].tokens, items[0].id)
		log.Printf("[dry-run] crearía/aseguraría colección de Qdrant con dim=%d (Coseno)", jina.Dim)
		log.Printf("[dry-run] OK: no se realizaron llamadas a Jina/Qdrant, no se gastaron tokens")
		return
	}

	cfg, err := config.Load(*envPath)
	if err != nil {
		log.Fatalf("configuración: %v", err)
	}
	// Configurar un contexto con cancelación (Graceful Shutdown).
	// Esto nos permite presionar Ctrl+C para interrumpir el proceso de ingesta.
	// La señal se propagará cancelando las peticiones a Jina y Qdrant en vuelo.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var embedder rag.Embedder = jina.New(cfg.EmbedderAPIKey)
	var store rag.VectorStore = qdrant.New(cfg.VectorStoreURL, cfg.VectorStoreAPIKey)

	// Asegurar que la colección exista
	log.Printf("Asegurando que la colección %q exista...", cfg.Collection)
	created, err := store.EnsureCollection(ctx, cfg.Collection, jina.Dim)
	if err != nil {
		log.Fatalf("asegurando colección %q: %v", cfg.Collection, err)
	}
	if created {
		log.Printf("creada colección de Qdrant %q (dim=%d, Coseno)", cfg.Collection, jina.Dim)
	} else {
		log.Printf("la colección de Qdrant %q ya existe", cfg.Collection)
	}

	lim := newLimiter(*tpm, *rpm)
	var done, spentTokens int

	for start := 0; start < len(items); {
		// Ensamblar un lote respetando el conteo y el límite de tokens por solicitud.
		end := start
		batchTokens := 0
		batch := items[start:end]
		for end < len(items) && end-start < *batchSize {
			nt := items[end].tokens
			if end > start && batchTokens+nt > *maxReqTokens {
				break
			}
			batchTokens += nt
			end++
		}
		batch = items[start:end]

		texts := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			texts = append(texts, items[i].text)
		}

		lim.wait(batchTokens) // pace para mantenerse bajo TPM/RPM

		vectors, tokens, err := embedder.Embed(ctx, jina.TaskPassage, texts)
		if err != nil {
			log.Fatalf("lote de embedding [%d:%d]: %v", start, end, err)
		}
		spentTokens += tokens

		// Construir puntos para Qdrant
		points := make([]rag.Point, 0, len(vectors))
		for i, v := range vectors {
			it := batch[i]
			points = append(points, rag.Point{ID: it.id, Vector: v, Payload: it.payload})
		}
		if err := store.Upsert(ctx, cfg.Collection, points); err != nil {
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

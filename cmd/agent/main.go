package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	
	ragagent "github.com/imjowend/go-stdlib-rag/internal/agent"
	
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func main() {
	envPath := flag.String("env", ".env", "path to .env file")
	jsonlPath := flag.String("jsonl", "data/stdlib_docs.jsonl", "path to the extracted JSONL (for showing examples)")
	flag.Parse()

	// 1. Validate environment
	if _, err := os.Stat(*jsonlPath); err != nil {
		log.Fatalf("%s not found: run cmd/extract first", *jsonlPath)
	}

	cfg, err := config.Load(*envPath)
	if err != nil {
		log.Fatalf("configuración: %v", err)
	}

	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey == "" {
		log.Fatalf("GEMINI_API_KEY no está configurada")
	}

	// 2. Cargar ejemplos
	examples, err := loadExamples(*jsonlPath)
	if err != nil {
		log.Fatalf("cargando ejemplos desde %s: %v", *jsonlPath, err)
	}

	// 3. Inicializar el contexto con soporte para cancelación (Graceful Shutdown).
	// Capturamos señales del sistema operativo (como Ctrl+C) para abortar limpiamente
	// cualquier operación de red en curso y salir de forma ordenada.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 4. Inicializar los clientes (Embedder y VectorStore)
	jc := jina.New(cfg.EmbedderAPIKey)
	qc := qdrant.New(cfg.VectorStoreURL, cfg.VectorStoreAPIKey)

	// 5. Inicializar el Agente ADK con el LLM y las dependencias inyectadas
	log.Println("Inicializando agente ADK...")
	agentInst, err := ragagent.NewAgent(ctx, geminiKey, jc, qc, cfg.Collection, examples)
	if err != nil {
		log.Fatalf("falló la creación del agente: %v", err)
	}

	sessionSvc := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName: "go-expert",
		Agent: agentInst,
		SessionService: sessionSvc,
		AutoCreateSession: true,
	})
	if err != nil {
		log.Fatalf("falló la creación del runner: %v", err)
	}

	// 6. Bucle REPL
	fmt.Println("\n=======================================================")
	fmt.Println("🤖 Agente Experto en Go Stdlib (Gemini + ADK + Qdrant)")
	fmt.Println("Escribe tu pregunta o 'exit' para salir.")
	fmt.Println("=======================================================")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == "exit" || text == "quit" {
			break
		}
		
		msg := &genai.Content{
			Role: "user",
			Parts: []*genai.Part{
				{Text: text},
			},
		}

		fmt.Print("\n[Agent]:\n")
		for ev, err := range r.Run(ctx, "local-user", "local-session", msg, adkagent.RunConfig{}) {
			if err != nil {
				fmt.Printf("\n[Error del Agente]: %v\n", err)
				break
			}
			if ev != nil && ev.Content != nil {
				for _, part := range ev.Content.Parts {
					if part.Text != "" {
						fmt.Print(part.Text)
					}
				}
			}
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("leyendo stdin: %v", err)
	}
}

// loadExamples reads the JSONL and indexes each symbol's examples.
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

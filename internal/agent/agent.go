package agent

import (
	"context"
	"fmt"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"

	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
	"github.com/imjowend/go-stdlib-rag/internal/rag"
	ourtools "github.com/imjowend/go-stdlib-rag/internal/tools"
)

// NewAgent crea y configura un nuevo agente de ADK basado en Gemini.
// Inyecta el modelo, el prompt del sistema (instrucciones) y la herramienta
// RAG configurada para acceder al motor de búsqueda (Embedder + VectorStore).
func NewAgent(ctx context.Context, apiKey string, embedder rag.Embedder, store rag.VectorStore, collection string, examples map[string][]docmodel.Example) (adkagent.Agent, error) {
	// Inicializar el modelo Gemini 3.6 Flash
	model, err := gemini.NewModel(ctx, "gemini-3.6-flash", &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("creating gemini model: %w", err)
	}

	// Instanciar nuestro manejador personalizado para la herramienta RAG
	ragToolHandler := ourtools.NewRAGTool(collection, embedder, store, examples)

	// Envolverlo como una Herramienta compatible con el ADK (ADK Tool)
	qTool, err := functiontool.New[ourtools.SearchDocsArgs, string](
		functiontool.Config{
			Name: "qdrant_search",
			Description: "Busca en la documentación de la biblioteca estándar de Go usando Jina y Qdrant.",
		},
		func(ctx adkagent.ToolContext, args ourtools.SearchDocsArgs) (string, error) {
			return ragToolHandler.Search(ctx, args)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("creating qdrant tool: %w", err)
	}

	// Definir las instrucciones del sistema (System Prompt)
	systemInstruction := `Actúa como un desarrollador Senior en Go.
Usa la herramienta qdrant_search SIEMPRE ANTES de responder a cualquier pregunta técnica sobre la biblioteca estándar de Go (stdlib). 
Incluso si crees saber la respuesta, utiliza la herramienta para validar y extraer ejemplos de código actualizados.
Asegúrate de que todo el código que entregues sea idiomático (Go 1.24+).
Explica tus respuestas de forma clara y directa, razonando paso a paso si la pregunta es compleja.`

	// Configurar e inicializar el agente
	cfg := llmagent.Config{
		Name: "go_expert",
		Model: model,
		Tools: []tool.Tool{qTool},
		Instruction: systemInstruction,
	}

	return llmagent.New(cfg)
}

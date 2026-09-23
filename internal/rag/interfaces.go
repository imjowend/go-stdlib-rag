// Package rag provee interfaces y estructuras agnósticas para el sistema RAG.
package rag

import "context"

// Embedder define el contrato para generar representaciones vectoriales a partir de texto.
type Embedder interface {
	Embed(ctx context.Context, task string, texts []string) ([][]float32, int, error)
}

// Point es un elemento genérico que representa un vector y sus metadatos a insertar en la base de datos vectorial.
type Point struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

// SearchResult representa un resultado individual devuelto por la búsqueda en la base de datos vectorial.
type SearchResult struct {
	ID      string         `json:"id"`
	Score   float32        `json:"score"`
	Payload map[string]any `json:"payload"`
}

// VectorStore define el contrato para interactuar de forma abstracta con cualquier base de datos vectorial.
type VectorStore interface {
	EnsureCollection(ctx context.Context, name string, dim int) (created bool, err error)
	EnsurePayloadIndex(ctx context.Context, name string, fields map[string]string) ([]string, error)
	Upsert(ctx context.Context, name string, points []Point) error
	Search(ctx context.Context, name string, vector []float32, topK int, filter map[string]any) ([]SearchResult, error)
}

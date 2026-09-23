// Package qdrant es un cliente mínimo para la API REST de Qdrant Cloud,
// implementado utilizando únicamente la biblioteca estándar. 
// Soporta las operaciones requeridas por este proyecto: asegurar que exista
// la colección, insertar/actualizar vectores (upsert) y búsqueda vectorial.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/imjowend/go-stdlib-rag/internal/rag"
)

// Client se comunica con un clúster de Qdrant Cloud a través de REST.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New devuelve un nuevo Client inicializado con la URL del clúster y la clave API.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Note: El cliente Qdrant ahora usa rag.Point en lugar de declarar un tipo Point propio.

// EnsureCollection crea la colección con la dimensión de vector especificada y
// métrica de distancia de Coseno si esta no existe. No hace nada si ya existe.
func (c *Client) EnsureCollection(ctx context.Context, name string, dim int) (created bool, err error) {
	status, _, err := c.do(ctx, http.MethodGet, "/collections/"+name, nil)
	if err != nil {
		return false, err
	}
	if status == http.StatusOK {
		return false, nil
	}

	body := map[string]any{
		"vectors": map[string]any{
			"size":     dim,
			"distance": "Cosine",
		},
	}
	status, respBody, err := c.do(ctx, http.MethodPut, "/collections/"+name, body)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("create collection %d: %s", status, truncate(respBody))
	}
	return true, nil
}

// EnsurePayloadIndex asegura que exista un índice para cada campo solicitado del payload
// (nombre de campo -> esquema de campo de Qdrant, p. ej. "keyword" o "bool").
//
// Primero obtiene la colección y revisa su payload_schema, luego crea SOLO los índices faltantes.
// En un estado estable (donde todos existen) cuesta solo un GET y cero operaciones de escritura.
// Los índices de payload son obligatorios para filtrar sobre esos campos porque Qdrant Cloud
// habilita el modo estricto (unindexed_filtering_retrieve=false).
// Devuelve los nombres de los campos que fueron realmente creados.
func (c *Client) EnsurePayloadIndex(ctx context.Context, name string, fields map[string]string) ([]string, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/collections/"+name, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("get collection %d: %s", status, truncate(body))
	}
	var info struct {
		Result struct {
			PayloadSchema map[string]json.RawMessage `json:"payload_schema"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("decoding collection info: %w", err)
	}

	var created []string
	for field, schema := range fields {
		if _, ok := info.Result.PayloadSchema[field]; ok {
			continue // already indexed
		}
		reqBody := map[string]any{"field_name": field, "field_schema": schema}
		st, rb, err := c.do(ctx, http.MethodPut, "/collections/"+name+"/index?wait=true", reqBody)
		if err != nil {
			return created, err
		}
		if st != http.StatusOK {
			return created, fmt.Errorf("create index %q %d: %s", field, st, truncate(rb))
		}
		created = append(created, field)
	}
	return created, nil
}

// Upsert inserta o actualiza un lote de vectores (points),
// esperando (wait=true) a que la operación se aplique completamente.
func (c *Client) Upsert(ctx context.Context, name string, points []rag.Point) error {
	body := map[string]any{"points": points}
	status, respBody, err := c.do(ctx, http.MethodPut, "/collections/"+name+"/points?wait=true", body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("upsert %d: %s", status, truncate(respBody))
	}
	return nil
}

// Note: El cliente Qdrant ahora usa rag.SearchResult directamente en lugar de su propia versión.

// Search devuelve los topK vectores más cercanos al vector proporcionado.
// Si el filtro no es nulo, este es pasado de forma transparente a Qdrant como objeto de filtro.
func (c *Client) Search(ctx context.Context, name string, vector []float32, topK int, filter map[string]any) ([]rag.SearchResult, error) {
	body := map[string]any{
		"vector":       vector,
		"limit":        topK,
		"with_payload": true,
	}
	if filter != nil {
		body["filter"] = filter
	}
	status, respBody, err := c.do(ctx, http.MethodPost, "/collections/"+name+"/points/search", body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("search %d: %s", status, truncate(respBody))
	}
	var parsed struct {
		Result []rag.SearchResult `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}
	return parsed.Result, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}

func truncate(b []byte) string {
	const max = 400
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}

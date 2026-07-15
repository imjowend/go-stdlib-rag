// Package qdrant is a minimal client for Qdrant Cloud's REST API, using only
// the standard library. It supports the operations needed by this project:
// ensuring a collection exists, upserting points, and vector search.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to a Qdrant Cloud cluster over REST.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New returns a Client for the given cluster URL and API key.
func New(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Point is a single vector with its ID and metadata payload.
type Point struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload"`
}

// EnsureCollection creates the collection with the given vector dimension and
// cosine distance if it does not already exist. It is a no-op if present.
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

// EnsurePayloadIndex makes sure a payload index exists for each requested
// field (field name -> Qdrant field schema, e.g. "keyword" or "bool").
//
// It first GETs the collection and inspects its payload_schema, then creates
// ONLY the missing indexes. In steady state (all present) it costs a single
// GET and no writes. Payload indexes are required to filter on those fields
// because Qdrant Cloud enables strict mode (unindexed_filtering_retrieve=false).
// It returns the names of the fields it actually created.
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

// Upsert inserts or updates a batch of points (waiting for the operation to
// be applied).
func (c *Client) Upsert(ctx context.Context, name string, points []Point) error {
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

// SearchResult is one hit returned by Search.
type SearchResult struct {
	ID      string         `json:"id"`
	Score   float32        `json:"score"`
	Payload map[string]any `json:"payload"`
}

// Search returns the topK nearest points to vector. If filter is non-nil it is
// passed through as a Qdrant filter object.
func (c *Client) Search(ctx context.Context, name string, vector []float32, topK int, filter map[string]any) ([]SearchResult, error) {
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
		Result []SearchResult `json:"result"`
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

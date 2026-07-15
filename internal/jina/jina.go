// Package jina is a minimal client for the Jina AI embeddings API
// (jina-embeddings-v3), using only the standard library.
package jina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

const (
	endpoint = "https://api.jina.ai/v1/embeddings"
	model    = "jina-embeddings-v3"

	// TaskPassage is the task type for indexing documents (asymmetric retrieval).
	TaskPassage = "retrieval.passage"

	// TaskQuery is the task type for the search query (asymmetric retrieval).
	TaskQuery = "retrieval.query"

	// Dim is the default output dimension of jina-embeddings-v3.
	Dim = 1024

	maxRetries = 5
)

// Client talks to the Jina embeddings API.
type Client struct {
	apiKey string
	http   *http.Client
}

// New returns a Client authenticated with the given API key.
func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 60 * time.Second},
	}
}

type embedRequest struct {
	Model         string   `json:"model"`
	Task          string   `json:"task"`
	LateChunking  bool     `json:"late_chunking"`
	EmbeddingType string   `json:"embedding_type"`
	Input         []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed returns one vector per input text for the given task. It retries on
// 429 and 5xx responses with exponential backoff (honoring Retry-After).
// The second return value is the number of tokens billed by the API.
func (c *Client) Embed(ctx context.Context, task string, texts []string) ([][]float32, int, error) {
	if len(texts) == 0 {
		return nil, 0, nil
	}
	reqBody, err := json.Marshal(embedRequest{
		Model:         model,
		Task:          task,
		LateChunking:  false,
		EmbeddingType: "float",
		Input:         texts,
	})
	if err != nil {
		return nil, 0, err
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff(attempt))
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue // network error: retry
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusOK:
			var er embedResponse
			if err := json.Unmarshal(body, &er); err != nil {
				return nil, 0, fmt.Errorf("decoding jina response: %w", err)
			}
			out := make([][]float32, len(texts))
			for _, d := range er.Data {
				if d.Index >= 0 && d.Index < len(out) {
					out[d.Index] = d.Embedding
				}
			}
			for i, v := range out {
				if len(v) == 0 {
					return nil, 0, fmt.Errorf("jina returned no embedding for input %d", i)
				}
			}
			return out, er.Usage.TotalTokens, nil

		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("jina %d: %s", resp.StatusCode, truncate(body))
			if ra := retryAfter(resp); ra > 0 {
				time.Sleep(ra)
			}
			continue

		default:
			return nil, 0, fmt.Errorf("jina %d: %s", resp.StatusCode, truncate(body))
		}
	}
	return nil, 0, fmt.Errorf("jina: exhausted retries: %w", lastErr)
}

func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt-1)) * time.Second // 1s,2s,4s,8s,16s
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	return base + jitter
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

func truncate(b []byte) string {
	const max = 300
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}

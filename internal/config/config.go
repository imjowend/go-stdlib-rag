// Package config loads runtime configuration (API keys, Qdrant URL and the
// target collection name) from a .env file and/or the process environment.
//
// The .env file is gitignored and never committed; only .env.example (with
// placeholders) is versioned.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DefaultCollection is the Qdrant collection name used by ingest and query.
const DefaultCollection = "go_stdlib_docs"

// Config holds the credentials and settings needed to talk to Jina and Qdrant.
type Config struct {
	JinaAPIKey   string
	QdrantURL    string
	QdrantAPIKey string
	Collection   string
}

// Load reads .env (if present at envPath) into the environment without
// overriding already-set variables, then builds a Config from the environment
// and validates that the required keys are present.
func Load(envPath string) (*Config, error) {
	if err := loadDotEnv(envPath); err != nil {
		return nil, err
	}

	cfg := &Config{
		JinaAPIKey:   strings.TrimSpace(os.Getenv("JINA_API_KEY")),
		QdrantURL:    strings.TrimRight(strings.TrimSpace(os.Getenv("QDRANT_URL")), "/"),
		QdrantAPIKey: strings.TrimSpace(os.Getenv("QDRANT_API_KEY")),
		Collection:   strings.TrimSpace(os.Getenv("QDRANT_COLLECTION")),
	}
	if cfg.Collection == "" {
		cfg.Collection = DefaultCollection
	}

	var missing []string
	if cfg.JinaAPIKey == "" {
		missing = append(missing, "JINA_API_KEY")
	}
	if cfg.QdrantURL == "" {
		missing = append(missing, "QDRANT_URL")
	}
	if cfg.QdrantAPIKey == "" {
		missing = append(missing, "QDRANT_API_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s (copy .env.example to .env and fill them in)", strings.Join(missing, ", "))
	}
	return cfg, nil
}

// loadDotEnv parses a simple KEY=VALUE .env file. Missing file is not an error
// (values may come from the real environment). Existing env vars win.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return sc.Err()
}

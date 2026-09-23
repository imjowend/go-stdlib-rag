// Package config carga la configuración en tiempo de ejecución (claves API, URL de Base de Datos y
// el nombre de la colección objetivo) desde un archivo .env y/o variables de entorno del sistema.
//
// El archivo .env debe ser ignorado en git (.gitignore) y nunca versionarse;
// solo el archivo .env.example (con datos falsos) se versiona.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// DefaultCollection es el nombre por defecto para la colección de la base de datos vectorial
// que utilizarán los procesos de ingestión (ingest) y consulta (query/agent).
const DefaultCollection = "go_stdlib_docs"

// Config almacena las credenciales y configuraciones necesarias para la generación
// de embeddings y el almacenamiento vectorial (Vector Store).
type Config struct {
	EmbedderAPIKey   string
	VectorStoreURL   string
	VectorStoreAPIKey string
	Collection       string
}

// Load lee el archivo .env (si existe en la ruta envPath) y lo carga en el entorno del sistema
// sin sobrescribir las variables que ya existan. Luego, construye una instancia de Config
// a partir del entorno y valida que las claves obligatorias estén presentes.
func Load(envPath string) (*Config, error) {
	if err := loadDotEnv(envPath); err != nil {
		return nil, err
	}

	cfg := &Config{
		EmbedderAPIKey:   strings.TrimSpace(os.Getenv("EMBEDDER_API_KEY")),
		VectorStoreURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("VECTOR_STORE_URL")), "/"),
		VectorStoreAPIKey: strings.TrimSpace(os.Getenv("VECTOR_STORE_API_KEY")),
		Collection:       strings.TrimSpace(os.Getenv("VECTOR_STORE_COLLECTION")),
	}
	if cfg.Collection == "" {
		cfg.Collection = DefaultCollection
	}

	var missing []string
	if cfg.EmbedderAPIKey == "" {
		missing = append(missing, "EMBEDDER_API_KEY")
	}
	if cfg.VectorStoreURL == "" {
		missing = append(missing, "VECTOR_STORE_URL")
	}
	if cfg.VectorStoreAPIKey == "" {
		missing = append(missing, "VECTOR_STORE_API_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s (copy .env.example to .env and fill them in)", strings.Join(missing, ", "))
	}
	return cfg, nil
}

// loadDotEnv analiza de forma sencilla un archivo .env con el formato CLAVE=VALOR.
// Si el archivo no existe, no genera error (los valores podrían venir del sistema real).
// Si una variable ya existe en el entorno, esta tiene prioridad y no se sobrescribe.
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

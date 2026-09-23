// Package chunk construye el texto del documento para el embedding y los metadatos (payload)
// para Qdrant para un solo símbolo de la stdlib, siguiendo la estrategia "un símbolo = un fragmento".
package chunk

import (
	"crypto/sha1"
	"fmt"
	"strings"

	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
)

const (
	// MaxDocTokens es un límite defensivo estimado sobre la cantidad de tokens de un chunk,
	// bien por debajo del límite de contexto de 8192 tokens de jina-embeddings-v3.
	// En la práctica, solo el gigante bloque const/var de syscall supera este valor.
	MaxDocTokens = 6000

	// charsPerToken es un ratio conservador de caracteres-por-token.
	// Intencionadamente SOBREESTIMA los tokens para que el limitador de tasa se mantenga a salvo
	// debajo del TPM (Tokens Per Minute) de Jina.
	charsPerToken = 3.5
)

// EstimateTokens devuelve una estimación conservadora del número de tokens de la cadena s.
func EstimateTokens(s string) int {
	return int(float64(len(s))/charsPerToken) + 1
}

// BuildText renderiza el texto para el embedding de un símbolo utilizando una
// plantilla uniforme. El nombre del paquete y la firma (signature) van primero
// para anclar semánticamente el símbolo; el comentario de documentación y los ejemplos
// ejecutables le siguen a continuación.
//
// Si el texto resultante superaría MaxDocTokens, ÚNICAMENTE la firma (signature) es
// truncada (el comentario de documentación siempre se preserva íntegro). Este es un
// sacrificio deliberado que en la práctica solo afecta a un solo símbolo: el enorme
// bloque de constantes de syscall. Por lo tanto, una consulta sobre una constante
// específica más allá del punto de corte podría recuperar un fragmento (chunk) incompleto.
// Esto es intencional, no es un error (bug).
func BuildText(s docmodel.Symbol) (text string, truncated bool) {
	text = render(s, s.Signature)
	if EstimateTokens(text) <= MaxDocTokens {
		return text, false
	}
	// Calcular cuántos caracteres truncar de la firma para respetar el límite de tokens.
	overflowTokens := EstimateTokens(text) - MaxDocTokens
	trimChars := int(float64(overflowTokens)*charsPerToken) + 64 // small safety margin
	sig := s.Signature
	if trimChars >= len(sig) {
		sig = ""
	} else {
		sig = sig[:len(sig)-trimChars]
	}
	sig = strings.TrimRight(sig, " \t\n,") + "\n\t// ... (signature truncated to fit embedding context limit)"
	return render(s, sig), true
}

func render(s docmodel.Symbol, signature string) string {
	var b strings.Builder
	b.WriteString("Package: ")
	b.WriteString(s.Package)
	b.WriteByte('\n')
	b.WriteString(signature)
	b.WriteByte('\n')
	if s.Doc != "" {
		b.WriteByte('\n')
		b.WriteString(s.Doc)
		b.WriteByte('\n')
	}
	for _, ex := range s.Examples {
		b.WriteString("\nExample")
		if ex.Name != "" {
			b.WriteByte(' ')
			b.WriteString(ex.Name)
		}
		b.WriteString(":\n")
		b.WriteString(ex.Code)
		b.WriteByte('\n')
		if ex.Output != "" {
			b.WriteString("Output:\n")
			b.WriteString(ex.Output)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Payload devuelve los metadatos almacenados junto a cada vector en la base de datos (Qdrant).
// Los campos package, symbol_name, kind, y has_example son utilizados para el filtrado
// en las búsquedas; recv, signature, y doc se almacenan para poder mostrar directamente
// los resultados de búsqueda sin necesidad de re-leer el archivo JSONL.
func Payload(s docmodel.Symbol) map[string]any {
	return map[string]any{
		"package":     s.Package,
		"symbol_name": s.Name,
		"kind":        s.Kind,
		"recv":        s.Recv,
		"has_example": len(s.Examples) > 0,
		"signature":   s.Signature,
		"doc":         s.Doc,
	}
}

// namespace es un UUID constante usado como espacio de nombres para derivar IDs determinísticos de manera segura.
var namespace = [16]byte{0x67, 0x6f, 0x2d, 0x73, 0x74, 0x64, 0x6c, 0x69, 0x62, 0x2d, 0x72, 0x61, 0x67, 0x00, 0x00, 0x01}

// PointID devuelve un ID tipo UUIDv5 de forma determinística para un símbolo,
// para asegurar que al volver a ejecutar la ingesta se actualice (upsert) en lugar
// de duplicar el mismo punto vectorial (point).
func PointID(s docmodel.Symbol) string {
	key := s.Package + "|" + s.Kind + "|" + s.Recv + "|" + s.Name
	h := sha1.Sum(append(namespace[:], []byte(key)...))
	h[6] = (h[6] & 0x0f) | 0x50 // version 5
	h[8] = (h[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

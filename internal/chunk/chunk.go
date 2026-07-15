// Package chunk builds the embedding document text and Qdrant payload for a
// single stdlib symbol, following the "one symbol = one chunk" strategy.
package chunk

import (
	"crypto/sha1"
	"fmt"
	"strings"

	"github.com/imjowend/go-stdlib-rag/internal/docmodel"
)

const (
	// MaxDocTokens is a defensive cap on a single chunk's estimated tokens,
	// well under jina-embeddings-v3's 8192-token context limit. In practice
	// only the giant syscall const/var block exceeds this.
	MaxDocTokens = 6000

	// charsPerToken is a conservative chars-per-token ratio. It intentionally
	// OVER-estimates tokens so the rate limiter stays safely under Jina's TPM.
	charsPerToken = 3.5
)

// EstimateTokens returns a conservative token estimate for s.
func EstimateTokens(s string) int {
	return int(float64(len(s))/charsPerToken) + 1
}

// BuildText renders the embedding document for a symbol using a uniform
// template. The package name and signature go first to anchor the symbol
// semantically; the doc comment and runnable examples follow.
//
// If the rendered text would exceed MaxDocTokens, ONLY the signature is
// truncated (the doc comment is always preserved). This is a deliberate,
// accepted trade-off that in practice affects a single symbol: the huge
// syscall const block. A query about a specific constant beyond the cut point
// may therefore retrieve an incomplete chunk. This is intentional, not a bug.
func BuildText(s docmodel.Symbol) (text string, truncated bool) {
	text = render(s, s.Signature)
	if EstimateTokens(text) <= MaxDocTokens {
		return text, false
	}
	// Compute how many characters to trim from the signature to fit the cap.
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

// Payload returns the metadata stored alongside each vector in Qdrant.
// The fields package, symbol_name, kind, and has_example are used for
// filtering in queries; recv, signature, and doc are stored so query
// results can be displayed without re-reading the JSONL.
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

// namespace is a fixed UUID namespace used to derive deterministic point IDs.
var namespace = [16]byte{0x67, 0x6f, 0x2d, 0x73, 0x74, 0x64, 0x6c, 0x69, 0x62, 0x2d, 0x72, 0x61, 0x67, 0x00, 0x00, 0x01}

// PointID returns a deterministic UUIDv5-style ID for a symbol so that
// re-running ingest upserts (rather than duplicates) the same point.
func PointID(s docmodel.Symbol) string {
	key := s.Package + "|" + s.Kind + "|" + s.Recv + "|" + s.Name
	h := sha1.Sum(append(namespace[:], []byte(key)...))
	h[6] = (h[6] & 0x0f) | 0x50 // version 5
	h[8] = (h[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

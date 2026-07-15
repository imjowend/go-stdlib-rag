// Package docmodel defines the shared data model for the extracted stdlib
// documentation. It is used by cmd/extract to write data/stdlib_docs.jsonl
// and by cmd/ingest / cmd/query to read it back.
package docmodel

// Symbol is one documented, exported stdlib symbol. Each line of
// data/stdlib_docs.jsonl is one Symbol encoded as JSON.
type Symbol struct {
	// Package is the import path, e.g. "context" or "net/http".
	Package string `json:"package"`
	// Kind is one of: func, type, const, var, method.
	Kind string `json:"kind"`
	// Name is the symbol name, e.g. "WithTimeout".
	Name string `json:"name"`
	// Recv is the receiver type for methods (without the pointer star),
	// e.g. "Client" for (*Client).Do. Empty for non-methods.
	Recv string `json:"recv,omitempty"`
	// Signature is the full declaration rendered from the AST
	// (function/method bodies stripped).
	Signature string `json:"signature"`
	// Doc is the associated doc comment, already cleaned by go/doc.
	Doc string `json:"doc"`
	// Examples holds runnable examples from *_test.go files, if any.
	Examples []Example `json:"examples,omitempty"`
}

// Example is a runnable example associated with a symbol or package.
type Example struct {
	// Name is the example suffix, e.g. "WithTimeout" or "" for the base example.
	Name string `json:"name,omitempty"`
	// Code is the runnable example source.
	Code string `json:"code"`
	// Output is the expected output declared via the // Output: comment.
	Output string `json:"output,omitempty"`
}

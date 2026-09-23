// Package docmodel define el modelo de datos compartido para la documentación
// extraída de la biblioteca estándar (stdlib). Es utilizado por cmd/extract
// para escribir data/stdlib_docs.jsonl y por cmd/ingest / cmd/query para leerla.
package docmodel

// Symbol representa un símbolo exportado y documentado de la stdlib.
// Cada línea de data/stdlib_docs.jsonl es un Symbol codificado como JSON.
type Symbol struct {
	// Package es la ruta de importación, p. ej. "context" o "net/http".
	Package string `json:"package"`
	// Kind es uno de los siguientes: func, type, const, var, method.
	Kind string `json:"kind"`
	// Name es el nombre del símbolo, p. ej. "WithTimeout".
	Name string `json:"name"`
	// Recv es el tipo del receptor para los métodos (sin el asterisco de puntero),
	// p. ej. "Client" para (*Client).Do. Estará vacío para símbolos que no sean métodos.
	Recv string `json:"recv,omitempty"`
	// Signature es la declaración completa generada desde el AST
	// (sin el cuerpo de las funciones o métodos).
	Signature string `json:"signature"`
	// Doc es el comentario de documentación asociado, ya limpiado por go/doc.
	Doc string `json:"doc"`
	// Examples contiene ejemplos ejecutables provenientes de archivos *_test.go, si los hay.
	Examples []Example `json:"examples,omitempty"`
}

// Example representa un ejemplo de código ejecutable asociado a un símbolo o paquete.
type Example struct {
	// Name es el sufijo del ejemplo, p. ej. "WithTimeout" o vacío "" para el ejemplo base.
	Name string `json:"name,omitempty"`
	// Code es el código fuente ejecutable del ejemplo.
	Code string `json:"code"`
	// Output es la salida esperada declarada mediante el comentario // Output:
	Output string `json:"output,omitempty"`
}

// Package codegen contains the MCP generator built on top of Goa evaluation and
// Goa's JSON-RPC codegen.
//
// # Codegen Philosophy
//
// MCP code generation is intentionally fail-fast and transport-closed:
// MCP-enabled services either generate a pure MCP surface or generation fails.
// The package derives all per-run state from the evaluated Goa roots, then
// builds a synthetic MCP service expression and lets Goa generate the JSON-RPC
// transport/client code that MCP needs.
//
// Where MCP needs behavior beyond Loom's standard JSON-RPC generator
// (tool/resource/prompt adapters, MCP-specific clients, helper packages), this
// package emits dedicated files or replaces stable named sections using
// evaluated generator data. Mount, handler, endpoint initialization, SSE, and
// client-constructor ownership all enforce exact upstream cardinality. The
// generator never inspects or rewrites rendered Go source.
package codegen

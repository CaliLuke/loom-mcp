// Package gateway provides a transport-agnostic server and client wrapper
// for model completion. It exposes composable middleware for unary and
// streaming requests and can be paired with any RPC layer by supplying
// provider and caller functions that operate on the runtime model types.
//
// The gateway has no provider wire error contract and does not classify context
// overflow. Provider-specific adapters own context_overflow normalization.
package gateway

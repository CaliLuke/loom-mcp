// Package codex provides a raw model.Provider for ChatGPT-backed Codex
// subscription access. It targets a private and volatile wire contract at a
// fixed trusted endpoint. Applications own login, token refresh, persistence,
// and credential policy and inject current credentials for each request.
//
// The provider supports SSE, fresh-per-request WebSockets, and WebSocket-first
// automatic selection with one safe pre-output SSE fallback. It does not read
// environment variables, Codex CLI state, home-directory files, or keychains,
// and it does not implement model.TokenCounter.
package codex

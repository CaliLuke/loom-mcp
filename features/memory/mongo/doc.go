// Package mongo registers MongoDB-backed memory storage for loom-mcp agents.
// Use clients/mongo to build the low-level client and pass it to NewStore.
//
// Current clients append immutable event-bucket documents. They retain read
// compatibility with the legacy single-document transcript representation, so
// deployments can upgrade without rewriting existing run history first. Reads
// return the combined history in stable timestamp order.
package mongo

package api

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"

// RecordContentInput carries ordered content to store before workflow adoption.
type RecordContentInput struct {
	// RootRunID is the shared storage scope of the run tree.
	RootRunID string
	// Items contains the exact bytes to store, in order.
	Items [][]byte
}

// RecordContentOutput is returned only after every input item has been stored.
type RecordContentOutput struct {
	// Refs addresses Items in the same order and with the same length.
	Refs []runstore.Hash
}

// QueryRootRunID is the workflow query used by content-producing signal clients.
const QueryRootRunID = "root_run_id"

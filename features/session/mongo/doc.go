// Package mongo provides a MongoDB-backed implementation of the agents runtime
// session store. Build the low-level client via features/session/mongo/clients/mongo
// and pass it to NewStore so higher-level services can persist run metadata outside
// the core runtime.
//
// Run writes go through multi-document transactions, so this store requires a
// replica set (MongoDB 4.0 or later) or a sharded cluster (MongoDB 4.2 or
// later). A standalone mongod is rejected when the client is constructed, which
// refuses read-only consumers too: any holder of the client can reach UpsertRun.
package mongo

# Embeddings

`runtime/embeddings` defines `Client` for single-text vectors and `BatchClient`
for batches. `NewOllama(baseURL, model, httpClient)` provides an Ollama client;
a nil HTTP client uses a 30-second timeout. `EmbedBatch` preserves input order,
rejects empty batches and checks vector count, dimension consistency and finite,
nonzero values. Requests disable silent text truncation. Responses are bounded
to 16 MiB. Errors omit provider bodies, URLs and text.

Use `Validate(vector, dimension)` to check a vector before persistence. Pass zero
when the dimension is determined by the model response.

Applications own credentials, text selection, chunking, tenant scope, index
metadata and persistence. Durable workers should make provider calls inside
retryable activities and commit results idempotently against the source content
identity. This package does not start workers or store vectors.

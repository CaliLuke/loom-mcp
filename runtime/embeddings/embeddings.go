// Package embeddings provides model clients for vector generation. Applications
// own text selection, credentials, indexing policy and persistence.
package embeddings

import (
	"context"
	"errors"
	"math"
)

// Client generates one vector for one text.
type Client interface {
	Embed(context.Context, string) ([]float64, error)
}

// BatchClient generates vectors in the same order as its input texts.
type BatchClient interface {
	Client
	EmbedBatch(context.Context, []string) ([][]float64, error)
}

// Validate rejects empty, non-finite and zero vectors, which cannot participate
// in cosine similarity. A positive dimension requires an exact length match.
func Validate(vector []float64, dimension int) error {
	if len(vector) == 0 || (dimension > 0 && len(vector) != dimension) {
		return errors.New("embedding dimension mismatch")
	}
	nonzero := false
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("embedding contains non-finite values")
		}
		nonzero = nonzero || value != 0
	}
	if !nonzero {
		return errors.New("embedding is a zero vector")
	}
	return nil
}

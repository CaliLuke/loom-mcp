package checkpoint

import (
	"crypto/sha256"
	"fmt"
)

// Handle names one collapsed part of a stored checkpoint record.
func Handle(part, recordID string) string {
	hash := sha256.Sum256([]byte(part + recordID))
	return fmt.Sprintf("rc-%x", hash[:4])
}

// CheckpointID identifies an immutable record with an eight-hex digest.
func CheckpointID(recordID string) string {
	hash := sha256.Sum256([]byte(recordID))
	return fmt.Sprintf("ck-%x", hash[:4])
}

// compactHandle preserves discovery when a counted aggregate cannot fit.
func compactHandle(part, recordID string) string {
	return fmt.Sprintf("… [%s %s]\n", part, Handle(part, recordID))
}

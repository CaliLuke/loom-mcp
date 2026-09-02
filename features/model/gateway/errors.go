package gateway

import "errors"

// ErrProviderRequired indicates that a model.Provider must be supplied.
var ErrProviderRequired = errors.New("model gateway: provider is required")

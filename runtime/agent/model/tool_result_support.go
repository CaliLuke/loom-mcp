package model

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
)

// UnsupportedPartError identifies a message part rejected before provider I/O.
type UnsupportedPartError struct {
	Provider, Model, Placement, Kind, Format, Reason string
}

// ToolResultPartSupport describes accepted byte uploads and document URI schemes.
// Zero byte limits mean no uploads of that kind are supported.
type ToolResultPartSupport struct {
	ImageFormats                    []ImageFormat
	DocumentFormats                 []DocumentFormat
	DocumentURISchemes              []string
	MaxImageBytes, MaxDocumentBytes int
}

// ToolResultPartsInfo is an optional capability resolved for each request model.
type ToolResultPartsInfo interface {
	ToolResultParts(ctx context.Context, req *Request) ToolResultPartSupport
}

// ProviderConfigurationInfo names the exact provider configuration of a request.
type ProviderConfigurationInfo interface {
	ProviderConfiguration(context.Context, *Request) string
}

// Capabilities selects the optional interfaces a decorated client exposes.
type Capabilities struct {
	ProviderConfiguration ProviderConfigurationInfo
	Counter               TokenCounter
	Info                  ContextInfo
	ToolResultParts       ToolResultPartsInfo
}

const unsupportedFormat = "format"

// ErrUnsupportedPart is matched by unsupported message-part errors.
var ErrUnsupportedPart = errors.New("model: part not supported")

// ValidateToolResultParts rejects unsupported nested attachments before provider I/O.
// unsupportedReason overrides the zero-support reason for model-specific exclusions.
func ValidateToolResultParts(req *Request, support ToolResultPartSupport, provider, modelID, unsupportedReason string) error {
	if req == nil {
		return nil
	}
	for _, msg := range req.Messages {
		if msg == nil {
			continue
		}
		for _, p := range msg.Parts {
			normalized, err := normalizeMessagePart(p)
			if err != nil {
				return err
			}
			result, ok := normalized.(ToolResultPart)
			if !ok || len(result.Parts) == 0 {
				continue
			}
			if err := validateToolResultPart(result); err != nil {
				return err
			}
			for _, nested := range result.Parts {
				accepted, reason := support.Accepts(nested)
				if accepted {
					continue
				}
				if reason == "provider" && unsupportedReason != "" {
					reason = unsupportedReason
				}
				failure := &UnsupportedPartError{Provider: provider, Model: modelID, Placement: "tool_result", Reason: reason}
				part, err := normalizeMessagePart(nested)
				if err != nil {
					return err
				}
				switch v := part.(type) {
				case ImagePart:
					failure.Kind = "image"
					failure.Format = string(v.Format)
				case DocumentPart:
					failure.Kind = "document"
					failure.Format = string(v.Format)
				}
				return failure
			}
		}
	}
	return nil
}

// Error describes the unsupported placement and reason.
func (e *UnsupportedPartError) Error() string {
	return fmt.Sprintf("%s: %s %s (%s) not supported by %s: %s", e.Provider, e.Placement, e.Kind, e.Format, e.Model, e.Reason)
}

// Is matches ErrUnsupportedPart independently of provider details.
func (e *UnsupportedPartError) Is(target error) bool {
	return target == ErrUnsupportedPart
}

// Accepts reports whether the attachment fits this support, or its rejection reason.
func (s ToolResultPartSupport) Accepts(p ToolResultContentPart) (bool, string) {
	if len(s.ImageFormats) == 0 && len(s.DocumentFormats) == 0 && len(s.DocumentURISchemes) == 0 {
		return false, "provider"
	}
	normalized, err := normalizeMessagePart(p)
	if err != nil {
		return false, unsupportedFormat
	}
	switch part := normalized.(type) {
	case ImagePart:
		if !slices.Contains(s.ImageFormats, part.Format) {
			return false, unsupportedFormat
		}
		if s.MaxImageBytes <= 0 || len(part.Bytes) > s.MaxImageBytes {
			return false, "too_large"
		}
	case DocumentPart:
		if part.URI != "" {
			uri, err := url.Parse(part.URI)
			if err != nil || !slices.Contains(s.DocumentURISchemes, uri.Scheme) {
				return false, "uri"
			}
		} else {
			if !slices.Contains(s.DocumentFormats, part.Format) {
				return false, unsupportedFormat
			}
			if s.MaxDocumentBytes <= 0 || len(part.Bytes) > s.MaxDocumentBytes {
				return false, "too_large"
			}
		}
	default:
		return false, unsupportedFormat
	}
	return true, ""
}

// Intersect returns sorted common formats/schemes and the smaller byte limits.
func (s ToolResultPartSupport) Intersect(o ToolResultPartSupport) ToolResultPartSupport {
	return ToolResultPartSupport{
		ImageFormats:       intersectFormats(s.ImageFormats, o.ImageFormats),
		DocumentFormats:    intersectFormats(s.DocumentFormats, o.DocumentFormats),
		DocumentURISchemes: intersectFormats(s.DocumentURISchemes, o.DocumentURISchemes),
		MaxImageBytes:      min(s.MaxImageBytes, o.MaxImageBytes), MaxDocumentBytes: min(s.MaxDocumentBytes, o.MaxDocumentBytes),
	}
}

func intersectFormats[T ~string](a, b []T) []T {
	var common []T
	for _, value := range a {
		if slices.Contains(b, value) {
			common = append(common, value)
		}
	}
	slices.Sort(common)
	return slices.Compact(common)
}

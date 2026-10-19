package control

import (
	"encoding/base64"
	"slices"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
)

// putComponent checks declared slot bounds before copying or decoding content.
func (s *Service) putComponent(q EditRequest) (candidate.Component, error) {
	if (q.Content == nil) == (q.ContentBase64 == nil) {
		return candidate.Component{}, invalid(jsonContent)
	}
	surface := s.cfg.Candidates.Discover().Surface
	var slot *candidate.Slot
	for i := range surface.Slots {
		if surface.Slots[i].Name == q.Slot {
			slot = &surface.Slots[i]
			break
		}
	}
	if slot == nil {
		return candidate.Component{}, invalid(jsonSlot)
	}
	if !slices.Contains(slot.MediaTypes, q.MediaType) {
		return candidate.Component{}, invalid(jsonMediaType)
	}
	limit := min(slot.MaxBytes, surface.MaxTotalBytes)
	data, err := editBytes(q, limit)
	if err != nil {
		return candidate.Component{}, err
	}
	return candidate.Component{Name: q.Name, Slot: q.Slot, MediaType: q.MediaType, Data: data, Dependencies: slices.Clone(q.Dependencies)}, nil
}

// editBytes preserves exact text and canonical binary bytes under the same cap.
func editBytes(q EditRequest, limit int64) ([]byte, error) {
	if q.Content != nil {
		if !utf8.ValidString(*q.Content) || int64(len(*q.Content)) > limit {
			return nil, invalid(jsonContent)
		}
		return []byte(*q.Content), nil
	}
	// The encoded upper bound is checked before allocating a decoded copy.
	encoded := *q.ContentBase64
	if int64(base64.StdEncoding.DecodedLen(len(encoded))) > limit+2 {
		return nil, invalid(jsonContentBase64)
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || int64(len(data)) > limit || base64.StdEncoding.EncodeToString(data) != encoded {
		return nil, invalid(jsonContentBase64)
	}
	return data, nil
}

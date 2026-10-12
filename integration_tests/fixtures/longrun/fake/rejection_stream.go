package fake

import (
	"io"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// rejectionStream exposes the same forced response through the validated stream boundary.
type rejectionStream struct {
	response *model.Response
	chunks   []model.Chunk
	eof      bool
}

func (s *rejectionStream) Recv() (model.Chunk, error) {
	if len(s.chunks) == 0 {
		s.eof = true
		return nil, io.EOF
	}
	c := s.chunks[0]
	s.chunks = s.chunks[1:]
	return c, nil
}
func (s *rejectionStream) Close() error {
	return nil
}
func (s *rejectionStream) Response() *model.Response {
	if !s.eof {
		return nil
	}
	return s.response
}

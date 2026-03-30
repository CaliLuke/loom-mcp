package boundedresult

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCanonicalFieldNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{FieldReturned, FieldTotal, FieldTruncated, FieldRefinementHint},
		CanonicalFieldNames(""),
	)
	assert.Equal(t,
		[]string{FieldReturned, FieldTotal, FieldTruncated, FieldRefinementHint, "next_cursor"},
		CanonicalFieldNames("next_cursor"),
	)
}

func TestRequiredFieldNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{FieldReturned, FieldTruncated}, RequiredFieldNames())
}

func TestOptionalFieldNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{FieldTotal, FieldRefinementHint},
		OptionalFieldNames(""),
	)
	assert.Equal(t,
		[]string{FieldTotal, FieldRefinementHint, "next_cursor"},
		OptionalFieldNames("next_cursor"),
	)
}

func TestHasContinuation(t *testing.T) {
	t.Parallel()

	cursor := "cursor-1"
	tests := []struct {
		name           string
		nextCursor     *string
		refinementHint string
		want           bool
	}{
		{
			name: "no cursor or hint",
			want: false,
		},
		{
			name:       "cursor only",
			nextCursor: &cursor,
			want:       true,
		},
		{
			name:           "hint only",
			refinementHint: "narrow your search",
			want:           true,
		},
		{
			name:           "cursor and hint",
			nextCursor:     &cursor,
			refinementHint: "narrow your search",
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, HasContinuation(tt.nextCursor, tt.refinementHint))
		})
	}
}

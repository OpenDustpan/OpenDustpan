package errcodetest_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestUncodedPathsElidedElements proves that an error body literal with no
// type of its own counts as an error body when the slice, the array or the map
// that holds it gives it the type of a body, also through a pointer, and that
// it has a code by the same rule as a literal with its type.
func TestUncodedPathsElidedElements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an element of a slice",
			src: `func writeSlice() any {
	return []errorResponse{{Message: "bad", Code: 400}}
}`,
			want: []string{"writeSlice: errorResponse{}"},
		},
		{
			name: "an element of an array",
			src: `func writeArray() any {
	return [...]errorResponse{{Message: "bad", Code: 400}}
}`,
			want: []string{"writeArray: errorResponse{}"},
		},
		{
			name: "an element of a slice of pointers",
			src: `func writePointer() any {
	return []*errorResponse{{Message: "bad", Code: 400}}
}`,
			want: []string{"writePointer: errorResponse{}"},
		},
		{
			name: "a value of a map",
			src: `func writeMapValue() any {
	return map[string]errorResponse{"x": {Message: "bad", Code: 400}}
}`,
			want: []string{"writeMapValue: errorResponse{}"},
		},
		{
			name: "a key of a map",
			src: `func writeMapKey() any {
	return map[errorResponse]int{{Message: "bad", Code: 400}: 1}
}`,
			want: []string{"writeMapKey: errorResponse{}"},
		},
		{
			name: "an element of an element",
			src: `func writeNested() any {
	return [][]errorResponse{{{Message: "bad", Code: 400}}}
}`,
			want: []string{"writeNested: errorResponse{}"},
		},
		{
			name: "an element of a slice of pointers in a map",
			src: `func writeMapSlicePointer() any {
	return map[string][]*errorResponse{"x": {{Message: "bad", Code: 400}}}
}`,
			want: []string{"writeMapSlicePointer: errorResponse{}"},
		},
		{
			name: "an element of a map of pointers in a slice",
			src: `func writeSliceMapPointer() any {
	return []map[string]*errorResponse{{"x": {Message: "bad", Code: 400}}}
}`,
			want: []string{"writeSliceMapPointer: errorResponse{}"},
		},
		{
			name: "an element with a code",
			src: `func writeCodedElement() any {
	return []errorResponse{{Message: "bad", Code: 400, ErrorCode: errcode.RequestInvalid}}
}`,
			want: nil,
		},
		{
			name: "an element of a slice of pointers in a map with a code",
			src: `func writeCodedMapSlicePointer() any {
	return map[string][]*errorResponse{"x": {{Message: "bad", Code: 400, ErrorCode: errcode.RequestInvalid}}}
}`,
			want: nil,
		},
		{
			name: "an element of a map of pointers in a slice with a code",
			src: `func writeCodedSliceMapPointer() any {
	return []map[string]*errorResponse{{"x": {Message: "bad", Code: 400, ErrorCode: errcode.RequestInvalid}}}
}`,
			want: nil,
		},
		{
			name: "an element of a slice of another type",
			src: `type item struct{ Name string }

func writeItems() any {
	return []item{{Name: "x"}}
}`,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := errcodetest.UncodedPaths(demo(writeSource(t, header+"\n"+tc.src+"\n")), coded())
			if err != nil {
				t.Fatalf("UncodedPaths: %v", err)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("paths = %q, want %q", got, tc.want)
			}
		})
	}
}

package tflat

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/stretchr/testify/assert"
)

func TestLabelsMatch(t *testing.T) {
	assert.True(t, labelsMatch([]string{"a", "b"}, []string{"a", "b"}))
	assert.False(t, labelsMatch([]string{"a"}, []string{"a", "b"}), "length mismatch")
	assert.False(t, labelsMatch([]string{"a", "x"}, []string{"a", "b"}), "value mismatch")
}

func TestFormatRange(t *testing.T) {
	// Empty filename -> "<unknown>".
	assert.Equal(t, "<unknown>", formatRange(hcl.Range{}))

	// Filename only (line == 0) -> just the filename.
	assert.Equal(t, "x.tf", formatRange(hcl.Range{Filename: "x.tf"}))

	// Full position info.
	r := hcl.Range{
		Filename: "x.tf",
		Start:    hcl.Pos{Line: 3, Column: 5, Byte: 0},
	}
	assert.Equal(t, "x.tf:3:5", formatRange(r))
}

func TestFindAttrRange_NilPF(t *testing.T) {
	got := findAttrRange(nil, "resource", []string{"aws_s3_bucket", "x"}, "for_each")
	assert.Equal(t, hcl.Range{}, got)
}

func TestFindAttrRange_NoSyntax(t *testing.T) {
	pf := &parsedFile{path: "p", name: "p"}
	got := findAttrRange(pf, "resource", []string{"aws_s3_bucket", "x"}, "for_each")
	assert.Equal(t, hcl.Range{}, got)
}

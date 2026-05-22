package tflat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestFindBlockRange_NilPF(t *testing.T) {
	assert.Equal(t, hcl.Range{}, findBlockRange(nil, "module", []string{"x"}))
}

func TestFindBlockRange_NoSyntax(t *testing.T) {
	pf := &parsedFile{path: "p", name: "p"}
	assert.Equal(t, hcl.Range{}, findBlockRange(pf, "module", []string{"x"}))
}

func TestFindBlockRange_NotFound(t *testing.T) {
	pf := &parsedFile{path: "p", name: "p", syntax: emptyBody}
	assert.Equal(t, hcl.Range{}, findBlockRange(pf, "module", []string{"missing"}))
}

func TestFindBlockRangeIn_NotFound(t *testing.T) {
	// All files searched; none contains the block.
	files := []*parsedFile{
		{path: "p1", name: "p1", syntax: emptyBody},
		{path: "p2", name: "p2", syntax: emptyBody},
	}
	assert.Equal(t, hcl.Range{}, findBlockRangeIn(files, "module", []string{"x"}))
}

// emptyBody is an empty hclsyntax body shared by tests above to avoid
// re-parsing trivial source in every test case.
var emptyBody = &hclsyntax.Body{}

func TestFindBlockRange_TypeAndLabelMismatch(t *testing.T) {
	// File with several top-level blocks; verify findBlockRange walks past
	// the non-matching ones (covers the `continue` branches).
	tmp := t.TempDir()
	src := []byte(`resource "aws_s3_bucket" "this" { bucket = "x" }
module "other" { source = "./x" }
module "want"  { source = "./y" }
`)
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x.tf"), src, 0644))
	files, err := parseDir(tmp)
	require.NoError(t, err)
	pf := files[0]

	// Walks past `resource`, past `module "other"`, returns `module "want"`.
	got := findBlockRange(pf, "module", []string{"want"})
	assert.Equal(t, 3, got.Start.Line)

	// All blocks scanned, none match labels -> empty range.
	got = findBlockRange(pf, "module", []string{"nope"})
	assert.Equal(t, hcl.Range{}, got)
}

package tflat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParentReferencesModules_NilModules exercises the early-return branch
// for when the rewriter has no modules map at all.
func TestParentReferencesModules_NilModules(t *testing.T) {
	got := parentReferencesModules(&parsedFile{}, &rewriter{})
	assert.Nil(t, got)
}

// TestFindAttrRange_Variants covers the per-branch behaviour of
// findAttrRange when called with mismatching labels / missing attribute.
func TestFindAttrRange_Variants(t *testing.T) {
	tmp := t.TempDir()
	src := []byte(`resource "aws_s3_bucket" "this" {
  for_each = toset(["a"])
  bucket   = "x"
}`)
	path := filepath.Join(tmp, "x.tf")
	require.NoError(t, os.WriteFile(path, src, 0644))
	files, err := parseDir(tmp)
	require.NoError(t, err)
	require.Len(t, files, 1)
	pf := files[0]

	// Match: returns a non-empty range.
	got := findAttrRange(pf, "resource", []string{"aws_s3_bucket", "this"}, "for_each")
	assert.NotEqual(t, hcl.Range{}, got)
	assert.Equal(t, 2, got.Start.Line)

	// Labels mismatch -> empty range.
	got = findAttrRange(pf, "resource", []string{"aws_s3_bucket", "OTHER"}, "for_each")
	assert.Equal(t, hcl.Range{}, got)

	// Block type mismatch -> empty range.
	got = findAttrRange(pf, "module", []string{"aws_s3_bucket", "this"}, "for_each")
	assert.Equal(t, hcl.Range{}, got)

	// Attribute not present -> empty range.
	got = findAttrRange(pf, "resource", []string{"aws_s3_bucket", "this"}, "no_such_attr")
	assert.Equal(t, hcl.Range{}, got)
}

// TestParseDir_NonExistent covers the os.ReadDir error path.
func TestParseDir_NonExistent(t *testing.T) {
	_, err := parseDir(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read dir")
}

// TestParseDir_FileReadError covers the os.ReadFile error path inside
// parseDir by chmod'ing a .tf file to be unreadable.
func TestParseDir_FileReadError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: chmod 0 doesn't prevent reads")
	}
	tmp := t.TempDir()
	p := filepath.Join(tmp, "x.tf")
	require.NoError(t, os.WriteFile(p, []byte("# ok"), 0644))
	require.NoError(t, os.Chmod(p, 0))
	t.Cleanup(func() { _ = os.Chmod(p, 0644) })
	_, err := parseDir(tmp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read ")
}

// TestCollectMovedForCall_LoadModuleError covers the loadModule error path
// inside collectMovedForCall — reachable by pointing the dirs map at a
// directory that doesn't exist on disk.
func TestCollectMovedForCall_LoadModuleError(t *testing.T) {
	dirs := map[string]string{"x": filepath.Join(t.TempDir(), "nope")}
	_, err := collectMovedForCall([]string{"x"}, "x", dirs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read dir")
}

// TestLoadModule_BlocksWithoutLabels covers the `len(labels) == 0` branches
// for `variable {}` and `output {}` (legal HCL syntactically, even though
// Terraform would reject it semantically).
func TestLoadModule_BlocksWithoutLabels(t *testing.T) {
	tmp := t.TempDir()
	src := []byte(`variable {}
output {}
module {}
`)
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x.tf"), src, 0644))
	lm, err := loadModule(tmp)
	require.NoError(t, err)
	// Anonymous variable/output/module blocks are silently ignored.
	assert.Empty(t, lm.variables)
	assert.Empty(t, lm.outputs)
	assert.Empty(t, lm.moduleCallNames)
}

// TestLoadModule_OutputWithoutValue covers the branch where the output
// block exists but has no `value = ...` attribute.
func TestLoadModule_OutputWithoutValue(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x.tf"),
		[]byte(`output "x" {}`), 0644))
	lm, err := loadModule(tmp)
	require.NoError(t, err)
	// Output without a `value` attribute is not recorded.
	_, ok := lm.outputs["x"]
	assert.False(t, ok)
}

// TestCollectMovedForCall_UnknownKey exercises the defensive error path
// when the directory map is missing the requested key. Flatten guards
// against this earlier, so the only way to reach it is a direct call.
func TestCollectMovedForCall_UnknownKey(t *testing.T) {
	_, err := collectMovedForCall([]string{"x"}, "x", map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `module "x" not found`)
}

// TestCollectMovedForCall_NestedUnknownKey covers the recursive error
// branch: a known module declares a nested module call, but the nested
// module's key isn't in the directory map.
func TestCollectMovedForCall_NestedUnknownKey(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x.tf"),
		[]byte(`module "child" { source = "./child" }`), 0644))
	dirs := map[string]string{"parent": tmp}
	_, err := collectMovedForCall([]string{"parent"}, "parent", dirs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `module "parent.child" not found`)
}

// TestExtractModuleCalls_LabelCount covers the `len(labels) != 1` skip
// branch: blocks named `module {}` or `module "a" "b" {}` are not real
// module calls and must be ignored.
func TestExtractModuleCalls_LabelCount(t *testing.T) {
	tmp := t.TempDir()
	src := []byte(`module {}
module "a" "b" {}
module "ok" { source = "./x" }
`)
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x.tf"), src, 0644))
	files, err := parseDir(tmp)
	require.NoError(t, err)
	calls := extractModuleCalls(files[0])
	require.Len(t, calls, 1, "only the well-formed module call is recognised")
	assert.Equal(t, "ok", calls[0].name)
}

// TestCloneAndRewriteResource_BadLabels covers the defensive error path
// when a resource block has the wrong number of labels.
func TestCloneAndRewriteResource_BadLabels(t *testing.T) {
	tmp := t.TempDir()
	// `resource "x" {}` has one label instead of two.
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x.tf"),
		[]byte(`resource "x" {}`), 0644))
	files, err := parseDir(tmp)
	require.NoError(t, err)
	var bad *hclwrite.Block
	for _, b := range files[0].file.Body().Blocks() {
		if b.Type() == "resource" {
			bad = b
			break
		}
	}
	require.NotNil(t, bad)
	_, err = cloneAndRewriteResource(bad, "p", &moduleCall{}, &rewriter{}, files[0])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected resource block labels")
}


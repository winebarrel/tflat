package tflat

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestResourceAddr_BadLabels covers the defensive empty-string return when
// resourceAddr sees a block with the wrong number of labels.
func TestResourceAddr_BadLabels(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x.tf"),
		[]byte(`resource "x" {}`), 0644))
	files, err := parseDir(tmp)
	require.NoError(t, err)
	for _, b := range files[0].file.Body().Blocks() {
		if b.Type() == "resource" {
			assert.Empty(t, resourceAddr(b))
		}
	}
}

// TestCheckAddressCollisions_IgnoresMalformed verifies that malformed
// resource/data blocks (wrong label count) are skipped by the collision
// check rather than crashing it.
func TestCheckAddressCollisions_IgnoresMalformed(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "x.tf"),
		// One-label resource and a label-less data block: both have an
		// empty resourceAddr() result and must be skipped.
		[]byte("resource \"x\" {}\ndata {}\n"), 0644))
	files, err := parseDir(tmp)
	require.NoError(t, err)
	require.NoError(t, checkAddressCollisions(files, nil))
}

func TestPathInside(t *testing.T) {
	assert.True(t, pathInside("/a/b", "/a/b"), "identical paths are 'inside'")
	assert.True(t, pathInside("/a/b", "/a/b/c"))
	assert.False(t, pathInside("/a/b", "/a/bx"), "prefix-only must not match")
	assert.False(t, pathInside("/a/b", "/a"))
	assert.False(t, pathInside("/a/b", "/c/d"), "unrelated siblings")

	// Filesystem root: a naive HasPrefix(child, parent+sep) check would
	// build "//" and reject every absolute path. The Rel-based
	// implementation handles this correctly.
	assert.True(t, pathInside("/", "/foo"))
	assert.True(t, pathInside("/", "/"))
}

// errWriter always errors; used to exercise WriteToStdout's error returns.
type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) {
	return 0, errBoom
}

// errAfterN succeeds on the first n writes and errors on the (n+1)th.
// Used to walk past WriteToStdout's banner/content writes and reach the
// per-iteration error branches that errWriter can't reach.
type errAfterN struct{ remaining int }

func (e *errAfterN) Write(p []byte) (int, error) {
	if e.remaining <= 0 {
		return 0, errBoom
	}
	e.remaining--
	return len(p), nil
}

var errBoom = errors.New("boom")

// TestResult_WriteToStdout_ErrorMidStream covers the Fprintln/Fprintf
// error branches inside WriteToStdout that fire on the second file.
func TestResult_WriteToStdout_ErrorMidStream(t *testing.T) {
	res := &Result{Files: []FileOutput{
		{Path: "a.tf", Content: []byte("a")},
		{Path: "b.tf", Content: []byte("b")},
	}}
	// The first file needs: one banner Fprintf + one content Write = 2 ok
	// writes. The Fprintln blank line between files is the 3rd write.
	err := res.WriteToStdout(&errAfterN{remaining: 2})
	require.Error(t, err)

	// Push further: succeed through the second file's banner Fprintf and
	// fail at its content w.Write to cover the final error branch.
	err = res.WriteToStdout(&errAfterN{remaining: 4})
	require.Error(t, err)
}

func TestAddrOwnerString(t *testing.T) {
	withLoc := addrOwner{
		desc: "parent file main.tf",
		loc:  hcl.Range{Filename: "main.tf", Start: hcl.Pos{Line: 3, Column: 1}},
	}
	assert.Equal(t, "parent file main.tf at main.tf:3:1", withLoc.String())

	withoutLoc := addrOwner{desc: "parent file main.tf"}
	assert.Equal(t, "parent file main.tf", withoutLoc.String())
}

// TestResult_WriteToStdout_WriterError covers the WriteToStdout error
// branches. Passing a writer that always fails surfaces the first error
// up.
func TestResult_WriteToStdout_WriterError(t *testing.T) {
	res := &Result{Files: []FileOutput{
		{Path: "a.tf", Content: []byte("x")},
		{Path: "b.tf", Content: []byte("y")},
	}}
	err := res.WriteToStdout(errWriter{})
	require.Error(t, err)
}

// TestCollectMovedForCall_LoadModuleError covers the loadModule error path
// inside collectMovedForCall. Reachable by pointing the dirs map at a
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
	_, err = mutateResource(bad, "p", &moduleCall{}, &rewriter{}, files[0])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected resource block labels")
}


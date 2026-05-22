package tflat_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tflat"
)

func TestFlatten_Simple(t *testing.T) {
	res, err := tflat.Flatten(&tflat.Options{
		Dir:       "testdata/simple",
		MovedFile: "moved.tf",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	// main.tf must have the module block commented out and the
	// module.web.bucket_id reference substituted with the renamed resource ref.
	got, ok := files["main.tf"]
	require.True(t, ok, "main.tf should be in output (files=%v)", keys(files))
	assert.Contains(t, got, "# module \"web\"",
		"original module block should be commented out")
	assert.Contains(t, got, "aws_s3_bucket.web_this.id",
		"module.web.bucket_id should be replaced with aws_s3_bucket.web_this.id")
	assert.NotContains(t, got, "module.web.bucket_id",
		"module.web.bucket_id reference should be gone")

	// web.tf must contain the inlined resource with prefix-renamed label and
	// var.bucket replaced by the caller's value.
	webTF, ok := files["web.tf"]
	require.True(t, ok, "web.tf should be in output")
	assert.Contains(t, webTF, "resource \"aws_s3_bucket\" \"web_this\"")
	assert.Contains(t, webTF, "\"my-bucket\"",
		"var.bucket should be substituted with caller's literal")
	assert.NotContains(t, webTF, "var.bucket")

	// moved.tf must include the moved block.
	moved, ok := files["moved.tf"]
	require.True(t, ok, "moved.tf should be in output")
	movedNorm := strings.Join(strings.Fields(moved), " ")
	assert.Contains(t, movedNorm, "from = module.web.aws_s3_bucket.this")
	assert.Contains(t, movedNorm, "to = aws_s3_bucket.web_this")
}

func TestFlatten_ForEach(t *testing.T) {
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/foreach"})
	require.NoError(t, err)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	webTF, ok := files["web.tf"]
	require.True(t, ok, "web.tf should be present")
	// for_each must be propagated onto the inlined resource.
	assert.Contains(t, webTF, "for_each = toset([\"a\", \"b\"])")
	// var.bucket -> each.value (the caller's expression).
	assert.Contains(t, webTF, "bucket   = each.value")

	moved, ok := files["moved.tf"]
	require.True(t, ok, "moved.tf should be present")
	movedNorm := strings.Join(strings.Fields(moved), " ")
	// Terraform 1.1+ supports key-less moved blocks that match all instances.
	assert.Contains(t, movedNorm, "from = module.web.aws_s3_bucket.this")
	assert.Contains(t, movedNorm, "to = aws_s3_bucket.web_this")
}

func TestFlatten_CrossModuleRef(t *testing.T) {
	// module "b" takes module.a.bucket_id as an argument. After flattening,
	// the reference inside b's inlined resource body must point at a's
	// renamed resource, not at the (now-gone) module.a output.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/cross"})
	require.NoError(t, err)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	bTF, ok := files["b.tf"]
	require.True(t, ok, "b.tf should be in output (files=%v)", keys(files))
	assert.Contains(t, bTF, "aws_s3_bucket.a_this.id",
		"cross-module ref must be rewritten to the new resource address")
	assert.NotContains(t, bTF, "module.a.bucket_id",
		"the original module.a.bucket_id reference must be gone")
}

func TestFlatten_Count(t *testing.T) {
	// Module call has count=3, resource inside has no count/for_each.
	// Count must be propagated onto the inlined resource.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/count"})
	require.NoError(t, err)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	webTF, ok := files["web.tf"]
	require.True(t, ok)
	assert.Contains(t, webTF, "count  = 3")
	assert.Contains(t, webTF, "resource \"aws_s3_bucket\" \"web_this\"")

	moved, ok := files["moved.tf"]
	require.True(t, ok)
	movedNorm := strings.Join(strings.Fields(moved), " ")
	assert.Contains(t, movedNorm, "from = module.web.aws_s3_bucket.this")
	assert.Contains(t, movedNorm, "to = aws_s3_bucket.web_this")
}

func TestFlatten_ResourceForEach(t *testing.T) {
	// Module call has no for_each, but the resource inside the module does.
	// The resource's own for_each must be preserved untouched.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/resource_foreach"})
	require.NoError(t, err)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	rsTF, ok := files["rs.tf"]
	require.True(t, ok, "rs.tf should be in output (files=%v)", keys(files))
	// The resource's own for_each survives; var.items was substituted.
	assert.Contains(t, rsTF, "for_each = toset((")
	assert.Contains(t, rsTF, "[\"a\", \"b\"]")
	assert.NotContains(t, rsTF, "var.items")
	// Resource still renamed with module-name prefix.
	assert.Contains(t, rsTF, "resource \"aws_s3_bucket\" \"rs_this\"")

	moved, ok := files["moved.tf"]
	require.True(t, ok)
	movedNorm := strings.Join(strings.Fields(moved), " ")
	assert.Contains(t, movedNorm, "from = module.rs.aws_s3_bucket.this")
	assert.Contains(t, movedNorm, "to = aws_s3_bucket.rs_this")
}

func TestFlatten_BothForEach_Error(t *testing.T) {
	// Both the module call AND the resource use for_each. This cannot be
	// expressed as a single Terraform resource (count+for_each forbidden,
	// for_each cannot be 2-D), so we surface a clear error.
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/both_foreach"})
	require.Error(t, err)
	msg := err.Error()
	// Diagnostic mentions both locations so the user knows where to edit.
	assert.Contains(t, msg, "both use repetition attributes")
	assert.Contains(t, msg, "testdata/both_foreach/main.tf:3:3",
		"module call's for_each location must be reported")
	assert.Contains(t, msg, "testdata/both_foreach/modules/rs/main.tf:6:3",
		"resource's for_each location must be reported")
}

func TestFlatten_Nested(t *testing.T) {
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/nested"})
	require.NoError(t, err)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	main, ok := files["main.tf"]
	require.True(t, ok)
	// module.outer.role_arn must be resolved through inner's output -> after
	// renaming becomes aws_iam_role.outer_inner_role.arn.
	assert.Contains(t, main, "aws_iam_role.outer_inner_role.arn")
	assert.NotContains(t, main, "module.outer.role_arn")

	outerTF, ok := files["outer.tf"]
	require.True(t, ok)
	// Inner's resource ended up here, renamed with the chained prefix, and
	// its var.name was substituted via outer's caller arg "hello".
	assert.Contains(t, outerTF, "resource \"aws_iam_role\" \"outer_inner_role\"")
	assert.Contains(t, outerTF, "\"hello\"")
	assert.NotContains(t, outerTF, "var.name")

	moved, ok := files["moved.tf"]
	require.True(t, ok)
	movedNorm := strings.Join(strings.Fields(moved), " ")
	assert.Contains(t, movedNorm, "from = module.outer.module.inner.aws_iam_role.role")
	assert.Contains(t, movedNorm, "to = aws_iam_role.outer_inner_role")
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

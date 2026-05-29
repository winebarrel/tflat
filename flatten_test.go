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
	// Both the module call and the resource use for_each. This cannot be
	// expressed as a single Terraform resource (count and for_each are
	// mutually exclusive, and for_each cannot be 2-D), so we error out.
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

func TestFlatten_VarDefault(t *testing.T) {
	// Module variable has a default; caller does not pass it.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/var_default"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mTF, ok := files["m.tf"]
	require.True(t, ok)
	// var.name was substituted with the module's default value.
	assert.Contains(t, mTF, `"default-name"`)
	assert.NotContains(t, mTF, "var.name")
}

func TestFlatten_CountConflict(t *testing.T) {
	// Module call has count and so does the inner resource. Same
	// diagnostic as the for_each conflict, exercising the count branch.
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/count_conflict"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "both use repetition attributes")
	assert.Contains(t, msg, "module call \"m\" has count")
	assert.Contains(t, msg, "resource aws_s3_bucket.this has count")
}

func TestFlatten_OutputChain(t *testing.T) {
	// b's output value is var.upstream_a, which the parent passed as
	// module.a.id. After pass-1, b's outputs map still contains the raw
	// module.a.id reference; the fixpoint loop in Flatten resolves it.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/chain"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mainTF, ok := files["main.tf"]
	require.True(t, ok)
	// module.b.derived_a -> (after chain resolution) aws_iam_role.a_r.id
	assert.Contains(t, mainTF, "role       = aws_iam_role.a_r.id",
		"resource ref must resolve through a -> b output chain")
	// Active (non-comment) content must not reference any module.* address.
	active := stripCommentLines(mainTF)
	assert.NotContains(t, active, "module.")
}

func TestFlatten_NestedCount(t *testing.T) {
	// Nested module call uses count; count must propagate to inner resources.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/nested_count"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	outerTF, ok := files["outer.tf"]
	require.True(t, ok)
	assert.Contains(t, outerTF, "resource \"aws_iam_role\" \"outer_inner_r\"")
	assert.Contains(t, outerTF, "count = 2")
}

func TestFlatten_ParentMalformedModuleBlock(t *testing.T) {
	// A parent file with a syntactically-valid `module {}` (no labels)
	// next to a real module call must not panic and must still flatten
	// the real one.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/parent_malformed"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mTF, ok := files["m.tf"]
	require.True(t, ok)
	assert.Contains(t, mTF, "resource \"aws_iam_role\" \"m_r\"")
}

func TestFlatten_NestedMalformedModuleBlock(t *testing.T) {
	// `module {}` (no label) inside a nested module dir must be silently
	// skipped without affecting the rest of the flatten.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/nested_malformed"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	outerTF, ok := files["outer.tf"]
	require.True(t, ok)
	assert.Contains(t, outerTF, "resource \"aws_iam_role\" \"outer_r\"")
}

func TestFlatten_NestedMissingKey(t *testing.T) {
	// A nested module call references a directory that isn't in
	// modules.json. flattenCall must error with a useful message.
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/nested_missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `module "outer.ghost" not found`)
}

func TestFlatten_NestedForEach(t *testing.T) {
	// outer module's nested module call uses for_each; the for_each
	// expression must propagate down to inner's resource.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/nested_foreach"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	outerTF, ok := files["outer.tf"]
	require.True(t, ok)
	assert.Contains(t, outerTF, "resource \"aws_iam_role\" \"outer_inner_r\"")
	assert.Contains(t, outerTF, "for_each = toset([\"x\", \"y\"])")
	assert.Contains(t, outerTF, "name     = each.value")
}

func TestFlatten_Locals(t *testing.T) {
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/locals"})
	require.NoError(t, err)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	mTF, ok := files["m.tf"]
	require.True(t, ok)
	// locals key renamed to m_full_name.
	assert.Contains(t, mTF, "m_full_name = ")
	// var.prefix substituted with the caller's literal "p".
	assert.Contains(t, mTF, `"p"`)
	// Resource still references the renamed local.
	assert.Contains(t, mTF, "bucket = local.m_full_name")
}

func TestFlatten_GenericBlock(t *testing.T) {
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/check_block"})
	require.NoError(t, err)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	mTF, ok := files["m.tf"]
	require.True(t, ok)
	// `check "account"` block (unknown to our switch) is preserved verbatim
	// with the rewriter applied to its assertion expression.
	assert.Contains(t, mTF, `check "account"`)
	assert.Contains(t, mTF, "data.aws_caller_identity.m_current.account_id")
}

func TestFlatten_SplitParent(t *testing.T) {
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/split_parent"})
	require.NoError(t, err)

	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}

	// outputs.tf has no module block but references module.m.bucket_id,
	// which must be rewritten via the second pass.
	outputsTF, ok := files["outputs.tf"]
	require.True(t, ok, "outputs.tf should be rewritten (files=%v)", keys(files))
	assert.Contains(t, outputsTF, "aws_s3_bucket.m_this.id")
	assert.NotContains(t, outputsTF, "module.m.bucket_id")

	// standalone.tf had no module block and no module ref; it should be
	// omitted from the result entirely.
	_, hasStandalone := files["standalone.tf"]
	assert.False(t, hasStandalone, "untouched files must not be re-emitted")
}

func TestFlatten_MissingRequiredVar(t *testing.T) {
	// Module declares a required variable (no default) but the caller does
	// not pass it. Must error rather than silently emitting `var.X` into
	// the flattened output (which would later fail terraform plan with a
	// less helpful "undeclared input variable" error).
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/missing_required_var"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "required variable")
	assert.Contains(t, msg, "\"required\"")
	assert.Contains(t, msg, "testdata/missing_required_var/main.tf:1:1")
}

func TestFlatten_AttributeOrderPreserved(t *testing.T) {
	// Source-order of resource attributes must be preserved after
	// flattening (not sorted alphabetically). In particular, `count` /
	// meta-args that the user put first must stay first.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/attr_order"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mTF, ok := files["m.tf"]
	require.True(t, ok)
	// Extract attribute names in the order they appear within the
	// resource block.
	idxCount := strings.Index(mTF, "count")
	idxAMI := strings.Index(mTF, "ami")
	idxIType := strings.Index(mTF, "instance_type")
	idxTags := strings.Index(mTF, "tags")
	idxVPC := strings.Index(mTF, "vpc_security_group_ids")
	require.True(t, idxCount > 0 && idxAMI > 0 && idxIType > 0 && idxTags > 0 && idxVPC > 0,
		"all attributes must be present")
	assert.Less(t, idxCount, idxAMI, "count must come before ami (source order)")
	assert.Less(t, idxAMI, idxIType, "ami must come before instance_type")
	assert.Less(t, idxIType, idxTags, "instance_type must come before tags")
	assert.Less(t, idxTags, idxVPC, "tags must come before vpc_security_group_ids")
}

func TestFlatten_TopLevelCommentsPreserved(t *testing.T) {
	// Top-level comments in the parent file (before/between/after blocks)
	// must survive the rewrite. Earlier implementations rebuilt the file
	// from blocks only and silently dropped comments.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/top_comments"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mainTF, ok := files["main.tf"]
	require.True(t, ok)
	assert.Contains(t, mainTF, "# File-level comment describing this stack's purpose.")
	assert.Contains(t, mainTF, "# Owned by the platform team.")
	assert.Contains(t, mainTF, "# Separator comment grouping unrelated resources below.")
	// And the module call is still commented out, the resource still there.
	assert.Contains(t, mainTF, "# module \"m\"")
	assert.Contains(t, mainTF, "resource \"aws_iam_role\" \"r\"")
}

func TestFlatten_BothMetaOnCall(t *testing.T) {
	// A module call cannot legitimately declare both count and for_each.
	// tflat must detect this up front instead of silently producing a
	// resource block that has both (which terraform later rejects).
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/both_meta_call"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "count")
	assert.Contains(t, msg, "for_each")
	assert.Contains(t, msg, "testdata/both_meta_call/main.tf")
}

func TestFlatten_MultipleResources(t *testing.T) {
	// One module with two resources; the second references the first via
	// `aws_s3_bucket.this.id`. Both must be renamed with the prefix and the
	// inner reference rewritten.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/multi_resource"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mTF, ok := files["m.tf"]
	require.True(t, ok)
	assert.Contains(t, mTF, "resource \"aws_s3_bucket\" \"m_this\"")
	assert.Contains(t, mTF, "resource \"aws_s3_bucket_policy\" \"m_p\"")
	assert.Contains(t, mTF, "bucket = aws_s3_bucket.m_this.id",
		"cross-resource ref must be rewritten to the prefixed name")
}

func TestFlatten_SharedSource(t *testing.T) {
	// Two module calls pointing at the same source directory must each
	// produce their own prefix-scoped copy.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/shared_source"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	aTF, ok := files["a.tf"]
	require.True(t, ok)
	assert.Contains(t, aTF, "resource \"aws_s3_bucket\" \"a_this\"")
	assert.Contains(t, aTF, `bucket = "a"`)
	bTF, ok := files["b.tf"]
	require.True(t, ok)
	assert.Contains(t, bTF, "resource \"aws_s3_bucket\" \"b_this\"")
	assert.Contains(t, bTF, `bucket = "b"`)
}

func TestFlatten_ProviderInModuleStripped(t *testing.T) {
	// `terraform` and `provider` blocks inside the module must not be
	// copied into the flattened output, since they would duplicate the
	// root config.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/provider_in_module"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mTF, ok := files["m.tf"]
	require.True(t, ok)
	assert.Contains(t, mTF, "resource \"aws_s3_bucket\" \"m_this\"")
	assert.NotContains(t, mTF, "terraform {")
	assert.NotContains(t, mTF, "required_providers")
	assert.NotContains(t, mTF, "provider \"aws\"")
}

func TestFlatten_DynamicBlock(t *testing.T) {
	// dynamic block survives flattening; var.X inside it gets substituted.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/dynamic_block"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mTF, ok := files["m.tf"]
	require.True(t, ok)
	assert.Contains(t, mTF, "dynamic \"ingress\"")
	assert.Contains(t, mTF, "[80, 443]",
		"var.ports must be substituted with the caller's literal list")
	assert.NotContains(t, mTF, "var.ports")
	assert.Contains(t, mTF, "from_port = ingress.value",
		"ingress.value reference inside content is preserved")
}

func TestFlatten_ParentDuplicateAddress_SameFile(t *testing.T) {
	// Both occurrences are in the same file. Each must report its own
	// line, not point at the first match twice.
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/parent_dup_same_file"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "declared twice in the parent")
	assert.Contains(t, msg, "main.tf:1:1", "first occurrence at line 1")
	assert.Contains(t, msg, "main.tf:5:1", "second occurrence at line 5")
}

func TestFlatten_MissingMultipleVars_DeterministicOrder(t *testing.T) {
	// Three required vars are missing. The diagnostic reports them in
	// sorted name order: `alpha` first regardless of map iteration order.
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/missing_multiple_vars"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `required variable "alpha"`,
		"sort order is deterministic: alpha < mike < zulu, so alpha errors first")
}

func TestFlatten_ParentDuplicateAddress(t *testing.T) {
	// Two parent files declare the same resource address. Terraform itself
	// would reject this; tflat surfaces it up front with both locations.
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/parent_dup"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "declared twice in the parent")
	assert.Contains(t, msg, "aws_s3_bucket.dup")
	assert.Contains(t, msg, "testdata/parent_dup/extra.tf:3:1")
	assert.Contains(t, msg, "testdata/parent_dup/main.tf:1:1")
}

func TestFlatten_DataAddressCollision(t *testing.T) {
	// Same collision check but for `data` blocks (covers the `data.TYPE.NAME`
	// branch in checkAddressCollisions).
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/data_collision"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, `address "data.aws_caller_identity.m_current"`)
	assert.Contains(t, msg, "parent file main.tf")
	assert.Contains(t, msg, `module call "m"`)
}

func TestFlatten_AddressCollision(t *testing.T) {
	// Parent already owns the address that the module's renamed resource
	// would take. Must surface a diagnostic with both source locations.
	_, err := tflat.Flatten(&tflat.Options{Dir: "testdata/collision"})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, `address "aws_s3_bucket.m_this" would collide`)
	assert.Contains(t, msg, "parent file main.tf at testdata/collision/main.tf:7:1",
		"parent block's position must be reported")
	assert.Contains(t, msg, `module call "m" at testdata/collision/main.tf:1:1`,
		"module call's position must be reported")
}

func TestFlatten_DataSourceHasNoMovedEntry(t *testing.T) {
	// Terraform does not honor `moved` blocks for data sources. They
	// are not stored in state and are re-read on every plan. The module
	// here mixes a data source and a resource; only the resource must
	// appear in moved.tf.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/data_no_moved"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	moved, ok := files["moved.tf"]
	require.True(t, ok, "moved.tf should be present (files=%v)", keys(files))
	movedNorm := strings.Join(strings.Fields(moved), " ")
	assert.Contains(t, movedNorm, "from = module.m.aws_iam_role.this")
	assert.Contains(t, movedNorm, "to = aws_iam_role.m_this")
	// Moved addresses are emitted as traversals (e.g.
	// `module.m.data.aws_iam_policy_document.assume`), so any
	// regression that re-introduces data-source moved entries would
	// contain the `data.` segment in the traversal form.
	assert.NotContains(t, movedNorm, "data.",
		"data sources must not get moved entries (any type)")
}

func TestFlatten_EmptyModule(t *testing.T) {
	// Module with no resources produces neither <name>.tf nor moved.tf.
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/empty_module"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	_, hasM := files["m.tf"]
	assert.False(t, hasM, "empty module should not emit m.tf (files=%v)", keys(files))
	_, hasMoved := files["moved.tf"]
	assert.False(t, hasMoved, "no resources => no moved.tf")
	// main.tf was still rewritten to comment out the module block.
	main, ok := files["main.tf"]
	require.True(t, ok)
	assert.Contains(t, main, "# module \"m\"")
}

func TestFlatten_Provisioner(t *testing.T) {
	// A resource with provisioner / lifecycle nested blocks must survive
	// flattening with token-level rewriting applied inside the nested
	// blocks (`${var.msg}` -> `${"hi"}`).
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/provisioner"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mTF, ok := files["m.tf"]
	require.True(t, ok)
	assert.Contains(t, mTF, "provisioner \"local-exec\"")
	assert.Contains(t, mTF, "lifecycle {")
	assert.Contains(t, mTF, "create_before_destroy = true")
	assert.Contains(t, mTF, `"hi"`, "var.msg substituted into provisioner.command")
	assert.NotContains(t, mTF, "var.msg")
}

func TestFlatten_MetaArgsIgnored(t *testing.T) {
	// `depends_on` and `providers` on a module call are meta-arguments;
	// they must not bleed into the inlined resources (they are call-site
	// concerns, not module-body data).
	res, err := tflat.Flatten(&tflat.Options{Dir: "testdata/meta_args"})
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range res.Files {
		files[f.Path] = string(f.Content)
	}
	mTF, ok := files["m.tf"]
	require.True(t, ok)
	assert.NotContains(t, mTF, "depends_on")
	assert.NotContains(t, mTF, "providers")
}

// stripCommentLines drops any line that begins (after leading whitespace)
// with '#', so assertions about active content don't trip over the
// commented-out original module block.
func stripCommentLines(s string) string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

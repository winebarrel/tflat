package tflat

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// moduleCall captures everything we need from a single `module "X" {}` block
// in the parent. Some attributes (source/version/count/for_each/providers/
// depends_on) are not user variable values and must be stripped from the args
// map.
type moduleCall struct {
	name     string
	args     map[string]hclwrite.Tokens // user-provided variable bindings
	count    hclwrite.Tokens            // nil if not present
	forEach  hclwrite.Tokens            // nil if not present
	block    *hclwrite.Block            // original block, for commenting out
	parentPF *parsedFile                // file the block lives in
}

var moduleReservedAttrs = map[string]bool{
	"source":     true,
	"version":    true,
	"count":      true,
	"for_each":   true,
	"providers":  true,
	"depends_on": true,
}

// extractModuleCalls finds all `module "X"` blocks in the given file.
func extractModuleCalls(pf *parsedFile) []*moduleCall {
	var calls []*moduleCall
	for _, b := range pf.file.Body().Blocks() {
		if b.Type() != "module" {
			continue
		}
		labels := b.Labels()
		if len(labels) != 1 {
			continue
		}
		mc := &moduleCall{
			name:     labels[0],
			args:     map[string]hclwrite.Tokens{},
			block:    b,
			parentPF: pf,
		}
		for name, attr := range b.Body().Attributes() {
			toks := cloneTokens(attr.Expr().BuildTokens(nil))
			switch name {
			case "count":
				mc.count = toks
			case "for_each":
				mc.forEach = toks
			default:
				if moduleReservedAttrs[name] {
					continue
				}
				mc.args[name] = toks
			}
		}
		calls = append(calls, mc)
	}
	return calls
}

// flattened is the result of expanding a single module call. The blocks are
// ready to be written as a new file; outputs are the rewritten output value
// expressions, indexed by output name, for the parent to substitute.
type flattened struct {
	blocks  []*hclwrite.Block
	outputs map[string]hclwrite.Tokens
}

// flattenCall fully expands a module call (recursively for nested module
// calls). prefix is the address prefix to apply to resource/locals names
// (e.g. "web" or "web_inner"). moduleKey is the dotted key used to look up
// the module's directory in modules.json.
func flattenCall(
	mc *moduleCall,
	prefix string,
	moduleKey string,
	dirs map[string]string,
) (*flattened, error) {
	// A module call may not legitimately declare both count and for_each.
	// Terraform would reject it, but we should not silently propagate both
	// onto the inlined resources.
	if mc.count != nil && mc.forEach != nil {
		return nil, fmt.Errorf(
			"module call %q declares both count and for_each (mutually exclusive) at %s",
			mc.name, formatRange(findBlockRange(mc.parentPF, "module", []string{mc.name})),
		)
	}

	dir, ok := dirs[moduleKey]
	if !ok {
		return nil, fmt.Errorf("module %q not found in .terraform/modules/modules.json (run `terraform init`?)", moduleKey)
	}
	lm, err := loadModule(dir)
	if err != nil {
		return nil, err
	}

	// Build var substitution map: caller args override, else module's
	// default. A variable that is required (no default) AND not provided by
	// the caller is a hard error — silently leaving `var.X` in the output
	// would produce a broken Terraform configuration that only fails at
	// `terraform plan` time with a less actionable message.
	vars := map[string]hclwrite.Tokens{}
	// Iterate variables in sorted name order so the "required variable
	// missing" diagnostic is deterministic when more than one is absent.
	varNames := make([]string, 0, len(lm.variables))
	for name := range lm.variables {
		varNames = append(varNames, name)
	}
	sort.Strings(varNames)
	for _, name := range varNames {
		def := lm.variables[name]
		if arg, ok := mc.args[name]; ok {
			vars[name] = arg
		} else if def != nil {
			vars[name] = def
		} else {
			return nil, fmt.Errorf(
				"module call %q does not provide required variable %q:\n"+
					"  module call: %s\n"+
					"  variable declared: %s",
				mc.name, name,
				formatRange(findBlockRange(mc.parentPF, "module", []string{mc.name})),
				formatRange(findBlockRangeIn(lm.files, "variable", []string{name})),
			)
		}
	}

	// Build resource-rename / locals-rename maps for this module's *own*
	// resources and locals up front: we need them to rewrite nested-module
	// call arguments (which may reference siblings inside the same module
	// scope) before recursing.
	resourceRename := map[string]string{}
	for addr := range lm.resourceAddrs {
		parts := strings.Split(addr, ".")
		if strings.HasPrefix(addr, "data.") {
			if len(parts) == 3 {
				resourceRename[addr] = prefix + "_" + parts[2]
			}
		} else {
			if len(parts) == 2 {
				resourceRename[addr] = prefix + "_" + parts[1]
			}
		}
	}
	localsRename := map[string]string{}
	for _, pf := range lm.files {
		for _, b := range pf.file.Body().Blocks() {
			if b.Type() != "locals" {
				continue
			}
			for name := range b.Body().Attributes() {
				localsRename[name] = prefix + "_" + name
			}
		}
	}

	// Scope rewriter for this module (without nested-module outputs yet,
	// which we'll fill in as we recurse). Used to resolve var/local/resource
	// references inside nested-module call arguments before passing them
	// down.
	nestedOutputs := map[string]hclwrite.Tokens{}
	scope := &rewriter{
		vars:           vars,
		resourceRename: resourceRename,
		localsRename:   localsRename,
		modules:        nestedOutputs,
	}

	// First: recursively flatten nested module calls inside this module.
	// We need their outputs to rewrite module.X.Y references inside the
	// current module's blocks.
	var nestedBlocks []*hclwrite.Block

	for _, pf := range lm.files {
		for _, b := range pf.file.Body().Blocks() {
			if b.Type() != "module" {
				continue
			}
			labels := b.Labels()
			if len(labels) != 1 {
				continue
			}
			child := &moduleCall{
				name:     labels[0],
				args:     map[string]hclwrite.Tokens{},
				block:    b,
				parentPF: pf,
			}
			for name, attr := range b.Body().Attributes() {
				// Resolve the arg expression through the *parent* module's
				// scope (var.X -> caller's value, local.X -> renamed,
				// resource refs -> prefixed) before passing down.
				toks := scope.rewriteTokens(attr.Expr().BuildTokens(nil))
				switch name {
				case "count":
					child.count = toks
				case "for_each":
					child.forEach = toks
				default:
					if moduleReservedAttrs[name] {
						continue
					}
					child.args[name] = toks
				}
			}

			cf, err := flattenCall(child, prefix+"_"+child.name, moduleKey+"."+child.name, dirs)
			if err != nil {
				return nil, err
			}
			nestedBlocks = append(nestedBlocks, cf.blocks...)
			for k, v := range cf.outputs {
				nestedOutputs[child.name+"."+k] = v
			}
		}
	}

	// At this point scope.modules has been mutated to contain all nested
	// outputs; reuse it as the final rewriter for this module's blocks.
	rw := scope

	// Now walk the module's blocks and emit transformed copies.
	var out []*hclwrite.Block
	for _, pf := range lm.files {
		for _, b := range pf.file.Body().Blocks() {
			switch b.Type() {
			case "variable", "output", "terraform", "provider":
				continue
			case "module":
				continue // handled via nestedBlocks
			case "resource", "data":
				nb, err := mutateResource(b, prefix, mc, rw, pf)
				if err != nil {
					return nil, err
				}
				out = append(out, nb)
			case "locals":
				nb := mutateLocals(b, prefix, rw)
				out = append(out, nb)
			default:
				// e.g. "moved", "import", "check" -- mutate in place with
				// the rewriter applied.
				mutateGeneric(b, rw)
				out = append(out, b)
			}
		}
	}
	out = append(out, nestedBlocks...)

	// Rewrite each output's value expression to be embeddable in the parent.
	outs := map[string]hclwrite.Tokens{}
	for name, valToks := range lm.outputs {
		outs[name] = rw.rewriteTokens(valToks)
	}

	return &flattened{blocks: out, outputs: outs}, nil
}

// mutateResource renames the second label, propagates the caller's
// count/for_each if any, and rewrites all attribute expressions IN PLACE
// on b. This preserves source ordering (including comments inside the
// block) which a clone-then-rebuild approach would destroy.
//
// Because loadModule re-parses the module directory on every call, the
// parsed file we are mutating is private to this flattenCall — no other
// caller is observing it.
func mutateResource(b *hclwrite.Block, prefix string, mc *moduleCall, rw *rewriter, pf *parsedFile) (*hclwrite.Block, error) {
	labels := b.Labels()
	if len(labels) != 2 {
		return nil, fmt.Errorf("unexpected %s block labels: %v", b.Type(), labels)
	}

	// Conflict check before any mutation so we don't leave the block in a
	// half-rewritten state on error.
	if mc.count != nil || mc.forEach != nil {
		if b.Body().GetAttribute("count") != nil {
			return nil, conflictError("count", b.Type(), labels, mc, pf)
		}
		if b.Body().GetAttribute("for_each") != nil {
			return nil, conflictError("for_each", b.Type(), labels, mc, pf)
		}
	}

	// Rename second label.
	b.SetLabels([]string{labels[0], prefix + "_" + labels[1]})

	// Propagate count/for_each. SetAttributeRaw on a new name appends at the
	// end of the body, which is the best we can do without low-level token
	// manipulation; the conventional "top of resource" placement is lost
	// for these propagated attributes but the body's overall ordering is
	// otherwise preserved.
	if mc.count != nil {
		b.Body().SetAttributeRaw("count", cloneTokens(mc.count))
	}
	if mc.forEach != nil {
		b.Body().SetAttributeRaw("for_each", cloneTokens(mc.forEach))
	}

	// Rewrite all attribute expressions in place (preserves position).
	rewriteBodyInPlace(b.Body(), rw)
	return b, nil
}

// conflictError builds a diagnostic-style message showing both the module
// call's repetition attribute and the resource's, with file:line:col so the
// user can find the two places to edit.
func conflictError(attrName, blockType string, labels []string, mc *moduleCall, resPF *parsedFile) error {
	resRange := findAttrRange(resPF, blockType, labels, attrName)
	// The module call's offending attr is either "count" or "for_each";
	// pick whichever is set on mc.
	callAttr := "count"
	if mc.forEach != nil {
		callAttr = "for_each"
	}
	callRange := findAttrRange(mc.parentPF, "module", []string{mc.name}, callAttr)

	return fmt.Errorf(
		"%s.%s and module %q both use repetition attributes (cannot flatten):\n"+
			"  module call %q has %s at %s\n"+
			"  resource %s.%s has %s at %s\n"+
			"  hint: a single Terraform resource cannot combine count+for_each, and for_each cannot be 2-dimensional. "+
			"Either remove one of the repetition attributes or leave this module un-flattened",
		labels[0], labels[1], mc.name,
		mc.name, callAttr, formatRange(callRange),
		labels[0], labels[1], attrName, formatRange(resRange),
	)
}

// mutateLocals renames each attribute key in a locals block to its
// prefixed form and rewrites the attribute expressions.
//
// hclwrite has no in-place rename for attribute names, so we re-emit the
// body via SetAttributeRaw. This loses the original attribute *order* and
// any comments inside the locals block — an accepted limitation for this
// block type. (Names are deterministic via sort to keep output stable.)
func mutateLocals(b *hclwrite.Block, prefix string, rw *rewriter) *hclwrite.Block {
	attrs := b.Body().Attributes()
	names := make([]string, 0, len(attrs))
	for n := range attrs {
		names = append(names, n)
	}
	sort.Strings(names)

	nb := hclwrite.NewBlock("locals", nil)
	for _, name := range names {
		toks := rw.rewriteTokens(attrs[name].Expr().BuildTokens(nil))
		nb.Body().SetAttributeRaw(prefix+"_"+name, toks)
	}
	return nb
}

// mutateGeneric applies the rewriter to a block's attributes/nested
// blocks in place. Used for unknown block types (moved/import/check etc)
// where we want to preserve everything verbatim except for token
// substitutions inside attribute expressions.
func mutateGeneric(b *hclwrite.Block, rw *rewriter) {
	rewriteBodyInPlace(b.Body(), rw)
}

// rewriteBodyInPlace walks body and re-runs rw over every attribute
// expression, mutating the body. Use this for a second-pass rewrite after a
// block has already been produced (e.g. to resolve cross-module references
// that weren't known when the block was originally emitted).
func rewriteBodyInPlace(body *hclwrite.Body, rw *rewriter) {
	for name, attr := range body.Attributes() {
		toks := rw.rewriteTokens(attr.Expr().BuildTokens(nil))
		body.SetAttributeRaw(name, toks)
	}
	for _, sub := range body.Blocks() {
		rewriteBodyInPlace(sub.Body(), rw)
	}
}


package tflat

import (
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// rewriter applies token-level substitutions while scanning an expression's
// token list. It is configured per-callsite to know:
//   - which variable arguments to substitute (var.X)
//   - which resource labels to rename (aws_foo.bar -> aws_foo.<prefix>_bar)
//   - whether to substitute module.X.Y references (only on the parent side)
type rewriter struct {
	// vars holds substitution tokens for var.<name>. Missing => leave as-is.
	vars map[string]hclwrite.Tokens
	// resourceRename: addr "aws_foo.bar" -> new name "<prefix>_bar".
	resourceRename map[string]string
	// localsRename: "foo" -> "<prefix>_foo" for local.<name>.
	localsRename map[string]string
	// modules: nested module.X.Y -> tokens. Only used in parent rewrite.
	modules map[string]hclwrite.Tokens
}

// rewriteTokens returns a new token list with substitutions applied.
// Substitutions look for these patterns in order:
//
//	var.NAME                       -> vars[NAME]
//	module.NAME.OUT                -> modules["NAME.OUT"]
//	module.NAME["k"].OUT           -> indexed form (not substituted; warn)
//	TYPE.OLDNAME                   -> TYPE.NEWNAME (label rename only)
func (r *rewriter) rewriteTokens(in hclwrite.Tokens) hclwrite.Tokens {
	out := make(hclwrite.Tokens, 0, len(in))
	i := 0
	for i < len(in) {
		// Try var.NAME
		if r.vars != nil && i+2 < len(in) &&
			isIdent(in[i], "var") &&
			isDot(in[i+1]) &&
			in[i+2].Type == hclsyntax.TokenIdent {
			if sub, ok := r.vars[string(in[i+2].Bytes)]; ok {
				spacesBefore := in[i].SpacesBefore
				embedded := substituteForEmbed(sub)
				if len(embedded) > 0 {
					embedded[0].SpacesBefore = spacesBefore
				}
				out = append(out, embedded...)
				i += 3
				continue
			}
		}

		// Try module.NAME.OUT
		if r.modules != nil && i+4 < len(in) &&
			isIdent(in[i], "module") &&
			isDot(in[i+1]) &&
			in[i+2].Type == hclsyntax.TokenIdent &&
			isDot(in[i+3]) &&
			in[i+4].Type == hclsyntax.TokenIdent {
			modName := string(in[i+2].Bytes)
			outName := string(in[i+4].Bytes)
			key := modName + "." + outName
			if sub, ok := r.modules[key]; ok {
				spacesBefore := in[i].SpacesBefore
				embedded := substituteForEmbed(sub)
				if len(embedded) > 0 {
					embedded[0].SpacesBefore = spacesBefore
				}
				out = append(out, embedded...)
				i += 5
				continue
			}
		}

		// Try local.NAME rename.
		if r.localsRename != nil && i+2 < len(in) &&
			isIdent(in[i], "local") &&
			isDot(in[i+1]) &&
			in[i+2].Type == hclsyntax.TokenIdent {
			if newName, ok := r.localsRename[string(in[i+2].Bytes)]; ok {
				out = append(out, in[i], in[i+1])
				renamed := *in[i+2]
				renamed.Bytes = []byte(newName)
				out = append(out, &renamed)
				i += 3
				continue
			}
		}

		// Try TYPE.OLDNAME rename. We do this on any Ident . Ident pair
		// where the type (first ident) is not a keyword like "var" / "module"
		// / "local" / "each" / "count" / "self" / "path" / "terraform" / "data".
		if r.resourceRename != nil && i+2 < len(in) &&
			in[i].Type == hclsyntax.TokenIdent &&
			isDot(in[i+1]) &&
			in[i+2].Type == hclsyntax.TokenIdent &&
			!isReservedRoot(string(in[i].Bytes)) {
			typ := string(in[i].Bytes)
			name := string(in[i+2].Bytes)
			addr := typ + "." + name
			if newName, ok := r.resourceRename[addr]; ok {
				out = append(out, in[i])
				out = append(out, in[i+1])
				renamed := *in[i+2]
				renamed.Bytes = []byte(newName)
				out = append(out, &renamed)
				i += 3
				continue
			}
		}

		// data.TYPE.NAME rename (4 idents form)
		if r.resourceRename != nil && i+4 < len(in) &&
			isIdent(in[i], "data") &&
			isDot(in[i+1]) &&
			in[i+2].Type == hclsyntax.TokenIdent &&
			isDot(in[i+3]) &&
			in[i+4].Type == hclsyntax.TokenIdent {
			typ := string(in[i+2].Bytes)
			name := string(in[i+4].Bytes)
			addr := "data." + typ + "." + name
			if newName, ok := r.resourceRename[addr]; ok {
				out = append(out, in[i], in[i+1], in[i+2], in[i+3])
				renamed := *in[i+4]
				renamed.Bytes = []byte(newName)
				out = append(out, &renamed)
				i += 5
				continue
			}
		}

		out = append(out, in[i])
		i++
	}
	return out
}

func isReservedRoot(s string) bool {
	switch s {
	case "var", "local", "module", "data", "each", "count", "self",
		"path", "terraform", "null":
		return true
	}
	return false
}

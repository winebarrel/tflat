package tflat

import (
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// isIdent reports whether t is an Ident token with the given name.
func isIdent(t *hclwrite.Token, name string) bool {
	return t.Type == hclsyntax.TokenIdent && string(t.Bytes) == name
}

// isDot reports whether t is a "." token.
func isDot(t *hclwrite.Token) bool {
	return t.Type == hclsyntax.TokenDot
}

// tokensEqual compares two token sequences by type and bytes, ignoring
// spacing. Used to detect fixpoint convergence when iterating rewrites.
func tokensEqual(a, b hclwrite.Tokens) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type || string(a[i].Bytes) != string(b[i].Bytes) {
			return false
		}
	}
	return true
}

// cloneTokens returns a deep copy of tokens (sharing nothing).
func cloneTokens(in hclwrite.Tokens) hclwrite.Tokens {
	out := make(hclwrite.Tokens, len(in))
	for i, t := range in {
		nt := *t
		nt.Bytes = append([]byte(nil), t.Bytes...)
		out[i] = &nt
	}
	return out
}

// stripLeadingSpaces zeroes the SpacesBefore on the first token so the
// returned sequence has no leading whitespace when embedded.
func stripLeadingSpaces(in hclwrite.Tokens) hclwrite.Tokens {
	if len(in) == 0 {
		return in
	}
	out := cloneTokens(in)
	out[0].SpacesBefore = 0
	return out
}

// identToken builds a single Ident token.
func identToken(name string) *hclwrite.Token {
	return &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(name)}
}

// dotToken builds a "." token.
func dotToken() *hclwrite.Token {
	return &hclwrite.Token{Type: hclsyntax.TokenDot, Bytes: []byte(".")}
}

// parenWrap wraps tokens with parens to keep precedence safe when embedded.
func parenWrap(in hclwrite.Tokens) hclwrite.Tokens {
	out := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOParen, Bytes: []byte("(")},
	}
	out = append(out, stripLeadingSpaces(in)...)
	out = append(out, &hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")})
	return out
}

// isSimplePrimary reports whether tokens form a single literal or
// traversal that does not need parenthesizing when substituted into a
// larger expression.
func isSimplePrimary(in hclwrite.Tokens) bool {
	if len(in) == 0 {
		return true
	}
	// Allow a single literal or ident, possibly with `.Ident` or `["x"]`
	// chains.
	i := 0
	first := in[i]
	switch first.Type {
	case hclsyntax.TokenIdent,
		hclsyntax.TokenNumberLit,
		hclsyntax.TokenOQuote, // quoted string starts here
		hclsyntax.TokenOHeredoc:
		// ok
	default:
		return false
	}
	// Heuristic: no binary operators in the stream.
	for _, t := range in {
		switch t.Type {
		case hclsyntax.TokenPlus, hclsyntax.TokenMinus, hclsyntax.TokenStar,
			hclsyntax.TokenSlash, hclsyntax.TokenPercent,
			hclsyntax.TokenEqualOp, hclsyntax.TokenNotEqual,
			hclsyntax.TokenLessThan, hclsyntax.TokenLessThanEq,
			hclsyntax.TokenGreaterThan, hclsyntax.TokenGreaterThanEq,
			hclsyntax.TokenAnd, hclsyntax.TokenOr,
			hclsyntax.TokenQuestion, hclsyntax.TokenColon:
			return false
		}
	}
	return true
}

// substituteForEmbed prepares replacement tokens for embedding into a
// larger expression. Wraps with parens unless the inner expression is a
// simple primary.
func substituteForEmbed(in hclwrite.Tokens) hclwrite.Tokens {
	clean := stripLeadingSpaces(cloneTokens(in))
	if isSimplePrimary(clean) {
		return clean
	}
	return parenWrap(clean)
}

package tflat

import (
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/stretchr/testify/assert"
)

func TestTokensEqual(t *testing.T) {
	a := hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("foo")},
	}
	b := hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("foo")},
	}
	assert.True(t, tokensEqual(a, b), "identical sequences are equal")

	// Lengths differ.
	assert.False(t, tokensEqual(a, hclwrite.Tokens{}), "length mismatch is unequal")

	// Same length, type differs.
	diffType := hclwrite.Tokens{
		{Type: hclsyntax.TokenNumberLit, Bytes: []byte("foo")},
	}
	assert.False(t, tokensEqual(a, diffType), "type mismatch is unequal")

	// Same length and type, bytes differ.
	diffBytes := hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("bar")},
	}
	assert.False(t, tokensEqual(a, diffBytes), "bytes mismatch is unequal")
}

func TestStripLeadingSpaces_Empty(t *testing.T) {
	out := stripLeadingSpaces(hclwrite.Tokens{})
	assert.Empty(t, out, "empty input returns empty output")
}

func TestStripLeadingSpaces_Clears(t *testing.T) {
	in := hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("x"), SpacesBefore: 4},
	}
	out := stripLeadingSpaces(in)
	assert.Equal(t, 0, out[0].SpacesBefore)
	// Original is untouched (cloneTokens copies).
	assert.Equal(t, 4, in[0].SpacesBefore)
}

func TestIsSimplePrimary(t *testing.T) {
	// Empty: trivially "simple" so we can no-op-wrap it.
	assert.True(t, isSimplePrimary(hclwrite.Tokens{}))

	// Plain identifier: simple.
	assert.True(t, isSimplePrimary(hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("x")},
	}))

	// Number literal: simple.
	assert.True(t, isSimplePrimary(hclwrite.Tokens{
		{Type: hclsyntax.TokenNumberLit, Bytes: []byte("42")},
	}))

	// Anything starting with an operator-ish first token is not simple.
	assert.False(t, isSimplePrimary(hclwrite.Tokens{
		{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
	}))

	// Identifier followed by a binary operator is not simple.
	assert.False(t, isSimplePrimary(hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("a")},
		{Type: hclsyntax.TokenPlus, Bytes: []byte("+")},
		{Type: hclsyntax.TokenNumberLit, Bytes: []byte("1")},
	}))

	// Ternary is also not simple.
	assert.False(t, isSimplePrimary(hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("a")},
		{Type: hclsyntax.TokenQuestion, Bytes: []byte("?")},
		{Type: hclsyntax.TokenIdent, Bytes: []byte("b")},
		{Type: hclsyntax.TokenColon, Bytes: []byte(":")},
		{Type: hclsyntax.TokenIdent, Bytes: []byte("c")},
	}))
}

func TestSubstituteForEmbed_WrapsCompoundExpr(t *testing.T) {
	in := hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("a")},
		{Type: hclsyntax.TokenPlus, Bytes: []byte("+")},
		{Type: hclsyntax.TokenIdent, Bytes: []byte("b")},
	}
	out := substituteForEmbed(in)
	// First token should be "(".
	assert.Equal(t, hclsyntax.TokenOParen, out[0].Type)
	// Last token should be ")".
	assert.Equal(t, hclsyntax.TokenCParen, out[len(out)-1].Type)
}

func TestSubstituteForEmbed_LeavesSimpleAlone(t *testing.T) {
	in := hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte("x")},
	}
	out := substituteForEmbed(in)
	assert.Len(t, out, 1, "simple primary is not wrapped")
	assert.Equal(t, hclsyntax.TokenIdent, out[0].Type)
}

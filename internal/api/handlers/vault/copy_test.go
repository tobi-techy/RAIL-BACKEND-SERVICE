package vault

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUserFacingCopyNeverMentionsInfrastructure is a guard, not a description.
//
// The retirement vault is marketed as an automated USD retirement plan. A user
// must never see crypto, a chain, a token, a wallet, a seed phrase or the
// provider's name — not in a title, not in an error, not in a status code
// message. This test parses every string literal the user can reach and fails
// the build if one of those words appears.
func TestUserFacingCopyNeverMentionsInfrastructure(t *testing.T) {
	banned := []string{
		"crypto", "web3", "blockchain", "defi", "nft",
		"token", "coin", "chain", "wallet", "seed", "gas fee",
		"glider", "usdc", "usdt", "solana", "caip", "onchain", "on-chain",
	}

	files := []string{
		"handlers.go",
		"../../routes/vault_routes.go",
		"../../../infrastructure/di/vault_wiring.go",
	}

	var checked int
	for _, file := range files {
		contents, err := os.ReadFile(file)
		require.NoErrorf(t, err, "read %s (run tests from the vault handlers directory)", file)

		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, contents, 0)
		require.NoErrorf(t, err, "parse %s", file)

		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			lower := strings.ToLower(value)
			for _, word := range banned {
				if strings.Contains(lower, word) {
					t.Errorf("%s contains user-facing copy %q which mentions %q", file, value, word)
				}
			}
			checked++
			return true
		})
	}

	assert.Positive(t, checked, "the guard must actually inspect string literals")
}

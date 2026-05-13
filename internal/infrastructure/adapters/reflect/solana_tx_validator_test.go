package reflect

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"testing"

	"github.com/mr-tron/base58"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestValidateReflectUserMintTransaction(t *testing.T) {
	wallet := base58.Encode(bytesOf(1))
	tx := buildTestMintTransaction(t, wallet, solanaTokenProgramID, usdcMint, 1_000_000)

	err := validateReflectUserMintTransaction(tx, wallet, decimal.NewFromInt(1), nil)

	require.NoError(t, err)
}

func TestValidateReflectUserMintTransactionAcceptsConfiguredReflectProgram(t *testing.T) {
	wallet := base58.Encode(bytesOf(1))
	reflectProgram := base58.Encode(bytesOf(9))
	tx := buildTestReflectProgramTransaction(t, wallet, reflectProgram)

	err := validateReflectUserMintTransaction(tx, wallet, decimal.NewFromInt(1), []string{reflectProgram})

	require.NoError(t, err)
}

func TestValidateReflectUserMintTransactionRejectsUnconfiguredReflectProgram(t *testing.T) {
	wallet := base58.Encode(bytesOf(1))
	reflectProgram := base58.Encode(bytesOf(9))
	tx := buildTestReflectProgramTransaction(t, wallet, reflectProgram)

	err := validateReflectUserMintTransaction(tx, wallet, decimal.NewFromInt(1), nil)

	require.ErrorContains(t, err, "unapproved Solana program")
}

func TestValidateReflectUserMintTransactionRejectsWrongFeePayer(t *testing.T) {
	wallet := base58.Encode(bytesOf(1))
	otherWallet := base58.Encode(bytesOf(2))
	tx := buildTestMintTransaction(t, wallet, solanaTokenProgramID, usdcMint, 1_000_000)

	err := validateReflectUserMintTransaction(tx, otherWallet, decimal.NewFromInt(1), nil)

	require.ErrorContains(t, err, "fee payer")
}

func TestValidateReflectUserMintTransactionRejectsUnknownProgram(t *testing.T) {
	wallet := base58.Encode(bytesOf(1))
	unknownProgram := base58.Encode(bytesOf(9))
	tx := buildTestMintTransaction(t, wallet, unknownProgram, usdcMint, 1_000_000)

	err := validateReflectUserMintTransaction(tx, wallet, decimal.NewFromInt(1), nil)

	require.ErrorContains(t, err, "unapproved Solana program")
}

func TestValidateReflectUserMintTransactionRejectsAmountMismatch(t *testing.T) {
	wallet := base58.Encode(bytesOf(1))
	tx := buildTestMintTransaction(t, wallet, solanaTokenProgramID, usdcMint, 2_000_000)

	err := validateReflectUserMintTransaction(tx, wallet, decimal.NewFromInt(1), nil)

	require.ErrorContains(t, err, "amount mismatch")
}

func TestValidateReflectUserMintTransactionSupportsAddressLookupTables(t *testing.T) {
	wallet := base58.Encode(bytesOf(1))
	tableAddress := base58.Encode(bytesOf(9))
	tx := buildTestMintTransactionWithLookupTable(t, wallet, tableAddress, 1_000_000)
	resolver := func(_ context.Context, address string) ([]string, error) {
		require.Equal(t, tableAddress, address)
		return []string{usdcMint}, nil
	}

	err := validateReflectUserMintTransactionWithResolver(context.Background(), tx, wallet, decimal.NewFromInt(1), nil, resolver)

	require.NoError(t, err)
}

func TestValidateReflectUserMintTransactionRequiresLookupResolver(t *testing.T) {
	wallet := base58.Encode(bytesOf(1))
	tableAddress := base58.Encode(bytesOf(9))
	tx := buildTestMintTransactionWithLookupTable(t, wallet, tableAddress, 1_000_000)

	err := validateReflectUserMintTransaction(tx, wallet, decimal.NewFromInt(1), nil)

	require.ErrorContains(t, err, "lookup table resolver")
}

func buildTestMintTransaction(t *testing.T, walletAddress, programID, mintAddress string, amount uint64) string {
	t.Helper()
	wallet := mustDecodePubkey(t, walletAddress)
	program := mustDecodePubkey(t, programID)
	mint := mustDecodePubkey(t, mintAddress)

	tx := []byte{1}
	tx = append(tx, make([]byte, 64)...)
	tx = append(tx, 1, 0, 1)
	tx = append(tx, 3)
	tx = append(tx, wallet...)
	tx = append(tx, mint...)
	tx = append(tx, program...)
	tx = append(tx, make([]byte, 32)...)
	tx = append(tx, 1)
	tx = append(tx, 2)
	tx = append(tx, 4, 0, 1, 0, 0)
	data := make([]byte, 10)
	data[0] = 12
	binary.LittleEndian.PutUint64(data[1:9], amount)
	data[9] = 6
	tx = append(tx, byte(len(data)))
	tx = append(tx, data...)
	return base64.StdEncoding.EncodeToString(tx)
}

func buildTestReflectProgramTransaction(t *testing.T, walletAddress, programID string) string {
	t.Helper()
	wallet := mustDecodePubkey(t, walletAddress)
	program := mustDecodePubkey(t, programID)

	tx := []byte{1}
	tx = append(tx, make([]byte, 64)...)
	tx = append(tx, 1, 0, 1)
	tx = append(tx, 2)
	tx = append(tx, wallet...)
	tx = append(tx, program...)
	tx = append(tx, make([]byte, 32)...)
	tx = append(tx, 1)
	tx = append(tx, 1)
	tx = append(tx, 1, 0)
	tx = append(tx, 8)
	tx = append(tx, []byte{1, 2, 3, 4, 5, 6, 7, 8}...)
	return base64.StdEncoding.EncodeToString(tx)
}

func buildTestMintTransactionWithLookupTable(t *testing.T, walletAddress, tableAddress string, amount uint64) string {
	t.Helper()
	wallet := mustDecodePubkey(t, walletAddress)
	program := mustDecodePubkey(t, solanaTokenProgramID)
	lookupTable := mustDecodePubkey(t, tableAddress)

	tx := []byte{1}
	tx = append(tx, make([]byte, 64)...)
	tx = append(tx, 0x80)
	tx = append(tx, 1, 0, 1)
	tx = append(tx, 2)
	tx = append(tx, wallet...)
	tx = append(tx, program...)
	tx = append(tx, make([]byte, 32)...)
	tx = append(tx, 1)
	tx = append(tx, 1)
	tx = append(tx, 4, 0, 2, 0, 0)
	data := make([]byte, 10)
	data[0] = 12
	binary.LittleEndian.PutUint64(data[1:9], amount)
	data[9] = 6
	tx = append(tx, byte(len(data)))
	tx = append(tx, data...)
	tx = append(tx, 1)
	tx = append(tx, lookupTable...)
	tx = append(tx, 0)
	tx = append(tx, 1, 0)
	return base64.StdEncoding.EncodeToString(tx)
}

func mustDecodePubkey(t *testing.T, address string) []byte {
	t.Helper()
	pubkey, err := base58.Decode(address)
	require.NoError(t, err)
	require.Len(t, pubkey, 32)
	return pubkey
}

func bytesOf(v byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = v
	}
	return out
}

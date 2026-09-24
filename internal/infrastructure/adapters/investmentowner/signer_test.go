package investmentowner

import (
	"context"
	"strings"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/rail-service/rail_service/internal/infrastructure/adapters/circle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSeed = "rail-test-master-seed-that-is-long-enough"

func newTestDerivedSigner(t *testing.T, prefix string) (*DerivedSigner, uuid.UUID) {
	t.Helper()
	signer, err := NewDerivedSigner(testSeed, prefix, entities.WalletChainSolana)
	require.NoError(t, err)
	return signer, uuid.New()
}

func TestNewDerivedSignerRejectsUnusableSeeds(t *testing.T) {
	_, err := NewDerivedSigner("", "", entities.WalletChainSolana)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "master seed")

	_, err = NewDerivedSigner("too-short", "", entities.WalletChainSolana)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 32 characters")
}

func TestDerivedSignerIsPerUserAndDeterministic(t *testing.T) {
	signer, userID := newTestDerivedSigner(t, "solana:localnet")
	otherUser := uuid.New()

	account, err := signer.OwnerAccount(context.Background(), userID)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(account, "solana:localnet:"), "account %q", account)

	// The same user always resolves to the same owner account...
	again, err := signer.OwnerAccount(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, account, again)

	// ...and a different user never does.
	otherAccount, err := signer.OwnerAccount(context.Background(), otherUser)
	require.NoError(t, err)
	assert.NotEqual(t, account, otherAccount)

	// A second instance built from the same seed derives the same account.
	second, err := NewDerivedSigner(testSeed, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)
	rebuilt, err := second.OwnerAccount(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, account, rebuilt)
}

func TestDerivedSignerDefaultsToMainnetPrefix(t *testing.T) {
	signer, userID := newTestDerivedSigner(t, "")
	account, err := signer.OwnerAccount(context.Background(), userID)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(account, "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp:"), "account %q", account)
}

func TestDerivedSignerSignsMessagesVerifiably(t *testing.T) {
	signer, userID := newTestDerivedSigner(t, "solana:localnet")
	key, err := signer.ownerKey(userID)
	require.NoError(t, err)

	message := "enroll:portfolio-123:2026-09-15"
	encoded, err := signer.SignSolanaMessage(context.Background(), userID, message)
	require.NoError(t, err)

	signature, err := solana.SignatureFromBase58(encoded)
	require.NoError(t, err)
	assert.True(t, key.PublicKey().Verify([]byte(message), signature),
		"the signature must verify against the derived owner key")

	// A different user's key must not verify the same message.
	otherKey, err := signer.ownerKey(uuid.New())
	require.NoError(t, err)
	assert.False(t, otherKey.PublicKey().Verify([]byte(message), signature))
}

func TestDerivedSignerRefusesEmptyMessage(t *testing.T) {
	signer, userID := newTestDerivedSigner(t, "solana:localnet")
	_, err := signer.SignSolanaMessage(context.Background(), userID, "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to sign")
}

func TestDerivedSignerSignsTransactions(t *testing.T) {
	signer, userID := newTestDerivedSigner(t, "solana:localnet")
	key, err := signer.ownerKey(userID)
	require.NoError(t, err)

	unsigned := newUnsignedTransfer(t, key.PublicKey())
	encoded, err := unsigned.ToBase64()
	require.NoError(t, err)

	signedBase64, err := signer.SignSolanaTransaction(context.Background(), userID, encoded)
	require.NoError(t, err)

	signed, err := solana.TransactionFromBase64(signedBase64)
	require.NoError(t, err)
	require.NoError(t, signed.VerifySignatures(), "the returned transaction must carry a valid signature")
	assert.Equal(t, key.PublicKey(), signed.Message.AccountKeys[0], "the owner key must be the fee payer")
}

func TestSignSolanaTransactionRejectsGarbage(t *testing.T) {
	signer, userID := newTestDerivedSigner(t, "solana:localnet")

	_, err := signer.SignSolanaTransaction(context.Background(), userID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to sign")

	_, err = signer.SignSolanaTransaction(context.Background(), userID, "not-a-transaction")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode transaction")
}

// ---------------------------------------------------------------------------
// Circle-backed signer
// ---------------------------------------------------------------------------

type fakeWalletLookup struct {
	wallet *entities.ManagedWallet
	err    error
}

func (f *fakeWalletLookup) GetWalletByUserAndChain(_ context.Context, userID uuid.UUID, _ entities.WalletChain) (*entities.ManagedWallet, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.wallet != nil && f.wallet.UserID == userID {
		return f.wallet, nil
	}
	return nil, nil
}

type fakeTransactionSigner struct {
	signed *circle.SignedTransaction
	err    error
	calls  int

	message      string
	messageSig   string
	messageErr   error
	messageCalls int
}

func (f *fakeTransactionSigner) SignTransaction(_ context.Context, _, _, _ string) (*circle.SignedTransaction, error) {
	f.calls++
	return f.signed, f.err
}

func (f *fakeTransactionSigner) SignMessage(_ context.Context, _, message string) (string, error) {
	f.messageCalls++
	f.message = message
	return f.messageSig, f.messageErr
}

func TestCircleSignerUsesTheUsersWalletAsOwner(t *testing.T) {
	userID := uuid.New()
	wallets := &fakeWalletLookup{wallet: &entities.ManagedWallet{
		UserID: userID, Chain: entities.WalletChainSolana,
		Address: "7Np41oeYqPefeNQEHSv1UDhYrehxin3NStELsSKCT4K2", CircleWalletID: "wallet-1",
	}}
	signer, err := NewCircleSigner(wallets, &fakeTransactionSigner{}, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)

	account, err := signer.OwnerAccount(context.Background(), userID)
	require.NoError(t, err)
	assert.Equal(t, "solana:localnet:7Np41oeYqPefeNQEHSv1UDhYrehxin3NStELsSKCT4K2", account)
}

func TestCircleSignerSignsMessagesWithTheCustodyWallet(t *testing.T) {
	userID := uuid.New()
	wallets := &fakeWalletLookup{wallet: &entities.ManagedWallet{
		UserID: userID, Chain: entities.WalletChainSolana, Address: "addr", CircleWalletID: "wallet-1",
	}}
	transactions := &fakeTransactionSigner{messageSig: "3W6r38STvZuBSmk2bbbct132SjEsYSARo3CJi3JQvNUaFoYu"}
	signer, err := NewCircleSigner(wallets, transactions, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)

	signed, err := signer.SignSolanaMessage(context.Background(), userID, "withdraw authorization text")
	require.NoError(t, err)
	assert.Equal(t, "3W6r38STvZuBSmk2bbbct132SjEsYSARo3CJi3JQvNUaFoYu", signed)
	assert.Equal(t, 1, transactions.messageCalls)
	assert.Equal(t, "withdraw authorization text", transactions.message, "the exact stage-1 text must reach the custody provider")
}

func TestCircleSignerRejectsEmptyAndSurfacesSigningErrors(t *testing.T) {
	userID := uuid.New()
	wallets := &fakeWalletLookup{wallet: &entities.ManagedWallet{
		UserID: userID, Chain: entities.WalletChainSolana, Address: "addr", CircleWalletID: "wallet-1",
	}}
	signer, err := NewCircleSigner(wallets, &fakeTransactionSigner{}, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)

	_, err = signer.SignSolanaMessage(context.Background(), userID, "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to sign")

	// A custody-side signing failure surfaces verbatim: callers must show the
	// real reason, never fall back to some other key the smart account rejects.
	failing := &fakeTransactionSigner{messageErr: ErrMessageSigningUnsupported}
	signer, err = NewCircleSigner(wallets, failing, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)
	_, err = signer.SignSolanaMessage(context.Background(), userID, "withdraw:100")
	require.ErrorIs(t, err, ErrMessageSigningUnsupported)
}

func TestCircleSignerPrefersSignedTransaction(t *testing.T) {
	userID := uuid.New()
	wallets := &fakeWalletLookup{wallet: &entities.ManagedWallet{
		UserID: userID, Chain: entities.WalletChainSolana, Address: "addr", CircleWalletID: "wallet-1",
	}}
	transactions := &fakeTransactionSigner{signed: &circle.SignedTransaction{
		Signature: "sig-only", SignedTransaction: "signed-tx",
	}}
	signer, err := NewCircleSigner(wallets, transactions, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)

	signed, err := signer.SignSolanaTransaction(context.Background(), userID, "base64tx")
	require.NoError(t, err)
	assert.Equal(t, "signed-tx", signed)
	assert.Equal(t, 1, transactions.calls)
}

func TestCircleSignerFallsBackToSignatureAndRejectsEmpty(t *testing.T) {
	userID := uuid.New()
	wallets := &fakeWalletLookup{wallet: &entities.ManagedWallet{
		UserID: userID, Chain: entities.WalletChainSolana, Address: "addr", CircleWalletID: "wallet-1",
	}}
	transactions := &fakeTransactionSigner{signed: &circle.SignedTransaction{Signature: "sig-only"}}
	signer, err := NewCircleSigner(wallets, transactions, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)

	signed, err := signer.SignSolanaTransaction(context.Background(), userID, "base64tx")
	require.NoError(t, err)
	assert.Equal(t, "sig-only", signed)

	transactions.signed = &circle.SignedTransaction{}
	_, err = signer.SignSolanaTransaction(context.Background(), userID, "base64tx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty signature")
}

func TestCircleSignerRequiresAWalletWithCustodyID(t *testing.T) {
	userID := uuid.New()
	noWallet := &fakeWalletLookup{}
	signer, err := NewCircleSigner(noWallet, &fakeTransactionSigner{}, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)

	_, err = signer.OwnerAccount(context.Background(), userID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no custody wallet")

	// A wallet without a Circle wallet id cannot sign either.
	partial := &fakeWalletLookup{wallet: &entities.ManagedWallet{
		UserID: userID, Chain: entities.WalletChainSolana, Address: "addr",
	}}
	signer, err = NewCircleSigner(partial, &fakeTransactionSigner{}, "solana:localnet", entities.WalletChainSolana)
	require.NoError(t, err)
	_, err = signer.SignSolanaTransaction(context.Background(), userID, "base64tx")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no custody wallet")
}

func TestCircleSignerRequiresDependencies(t *testing.T) {
	_, err := NewCircleSigner(nil, &fakeTransactionSigner{}, "", entities.WalletChainSolana)
	require.Error(t, err)

	_, err = NewCircleSigner(&fakeWalletLookup{}, nil, "", entities.WalletChainSolana)
	require.Error(t, err)
}

func TestOwnerAccountIDPassesThroughCAIP10(t *testing.T) {
	cfg := newSignerConfig("solana:localnet", entities.WalletChainSolana)
	account, err := cfg.ownerAccountID("solana:mainnet:abc")
	require.NoError(t, err)
	assert.Equal(t, "solana:mainnet:abc", account)

	_, err = cfg.ownerAccountID("  ")
	require.Error(t, err)
}

func newUnsignedTransfer(t *testing.T, payer solana.PublicKey) *solana.Transaction {
	t.Helper()
	instruction := system.NewTransferInstruction(1_000_000, payer, solana.NewWallet().PublicKey()).Build()
	tx, err := solana.NewTransaction(
		[]solana.Instruction{instruction},
		solana.MustHashFromBase58("11111111111111111111111111111111"),
		solana.TransactionPayer(payer),
	)
	require.NoError(t, err)
	return tx
}

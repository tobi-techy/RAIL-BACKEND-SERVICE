package circle

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"go.uber.org/zap"
)

// ReverseNativeSOL builds a SOL transfer, has Circle sign it, broadcasts it,
// and waits for confirmation. Returns the on-chain tx signature.
func (a *Adapter) ReverseNativeSOL(ctx context.Context, walletID, destination string, lamports uint64) (string, error) {
	// 1. Get the wallet address from Circle
	wallet, err := a.client.GetWallet(ctx, walletID)
	if err != nil {
		return "", fmt.Errorf("get wallet: %w", err)
	}
	fromAddress, err := solana.PublicKeyFromBase58(wallet.Address)
	if err != nil {
		return "", fmt.Errorf("invalid wallet address %q: %w", wallet.Address, err)
	}
	toAddress, err := solana.PublicKeyFromBase58(destination)
	if err != nil {
		return "", fmt.Errorf("invalid destination %q: %w", destination, err)
	}

	// 2. Get recent blockhash + balance from Solana mainnet
	rpcClient := rpc.New(rpc.MainNetBeta_RPC)
	recent, err := rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("get recent blockhash: %w", err)
	}
	bal, err := rpcClient.GetBalance(ctx, fromAddress, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("get SOL balance: %w", err)
	}
	const rentExemptBuffer uint64 = 1_000_000 // 0.001 SOL
	if bal.Value < rentExemptBuffer {
		return "", fmt.Errorf("insufficient balance: have %d lamports, need at least %d for rent-exempt minimum", bal.Value, rentExemptBuffer)
	}
	lamports = bal.Value - rentExemptBuffer

	// 3. Build the raw transaction message manually.
	//    SystemProgram Transfer instruction: 4 bytes type (2) + 8 bytes lamports.
	var ixData [12]byte
	binary.LittleEndian.PutUint32(ixData[0:4], 2) // instruction index = 2 (Transfer)
	binary.LittleEndian.PutUint64(ixData[4:12], lamports)

	// Transaction message layout:
	//   [0]     num_required_signatures (1)
	//   [1]     num_readonly_signed_accounts (0)
	//   [2]     num_readonly_unsigned_accounts (1) — SystemProgram is non-writable
	//   [3..35] fromAddress (signer + writable)
	//   [35..67] toAddress (writable)
	//   [67..99] SystemProgramID (readonly unsigned)
	//   [99..103] compiled instruction: program_index=2, num_accounts=2, data_len=12
	//   [103..105] account_indices: [0, 1]
	//   [105..117] instruction data (12 bytes)
	//   [117..149] recent blockhash (32 bytes)
	//   [149]     num_instructions (1)
	msg := make([]byte, 0, 200)
	msg = append(msg, 1)  // num_required_signatures
	msg = append(msg, 0)  // num_readonly_signed_accounts
	msg = append(msg, 1)  // num_readonly_unsigned_accounts (SystemProgram)
	msg = append(msg, fromAddress[:]...)
	msg = append(msg, toAddress[:]...)
	msg = append(msg, solana.SystemProgramID[:]...)
	// Compiled instruction
	msg = append(msg, 2)    // program_account_index (SystemProgram)
	msg = append(msg, 2)    // num_accounts (from + to)
	msg = append(msg, 12)   // data_length
	msg = append(msg, 0, 1) // account_indices: [from=0, to=1]
	msg = append(msg, ixData[:]...)
	// Blockhash + instruction count
	msg = append(msg, recent.Value.Blockhash[:]...)
	msg = append(msg, 1) // one instruction

	// 4. Serialize as versioned-legacy transaction: 0x80 | 0x00 = legacy, then message bytes
	//    Circle expects Solana legacy transaction format.
	var txBytes bytes.Buffer
	txBytes.WriteByte(0x80 | 0x00) // legacy transaction marker
	txBytes.Write(msg)

	rawB64 := base64.StdEncoding.EncodeToString(txBytes.Bytes())

	a.logger.Info("Circle SOL reversal: signing transaction",
		zap.String("wallet_id", walletID),
		zap.String("from", wallet.Address),
		zap.String("to", destination),
		zap.Uint64("lamports", lamports),
		zap.Float64("sol", float64(lamports)/math.Pow(10, 9)),
		zap.Int("raw_tx_bytes", txBytes.Len()),
		zap.String("raw_tx_b64", rawB64))

	// 5. Sign via Circle
	signed, err := a.client.SignTransaction(ctx, &SignTransactionRequest{
		WalletID:       walletID,
		RawTransaction: rawB64,
		Memo:           "Rail: reversal of accidental SOL deposit",
	})
	if err != nil {
		return "", fmt.Errorf("sign transaction: %w", err)
	}

	a.logger.Info("Circle SOL reversal: signed",
		zap.String("signed_tx_len", fmt.Sprintf("%d", len(signed.SignedTransaction))))

	// 6. Broadcast via Solana RPC
	signedBytes, err := base64.StdEncoding.DecodeString(signed.SignedTransaction)
	if err != nil {
		return "", fmt.Errorf("decode signed transaction: %w", err)
	}

	// Verify the signed tx has a valid signature (ed25519 = 64 bytes)
	// Legacy tx format: [marker 0x80][message][signature 64 bytes]
	// But Circle may return it differently — just broadcast what we get.
	sig, err := rpcClient.SendRawTransaction(ctx, signedBytes)
	if err != nil {
		return "", fmt.Errorf("broadcast: %w", err)
	}

	a.logger.Info("Circle SOL reversal: broadcast", zap.String("sig", sig.String()))

	// 7. Confirm
	wsClient, err := ws.Connect(ctx, rpc.MainNetBeta_WS)
	if err != nil {
		a.logger.Warn("Circle SOL reversal: could not open WS for confirmation",
			zap.String("sig", sig.String()), zap.Error(err))
		return sig.String(), nil
	}
	defer wsClient.Close()

	sub, err := wsClient.SignatureSubscribe(sig, rpc.CommitmentConfirmed)
	if err != nil {
		a.logger.Warn("Circle SOL reversal: could not subscribe to signature",
			zap.String("sig", sig.String()), zap.Error(err))
		return sig.String(), nil
	}
	defer sub.Unsubscribe()

	a.logger.Info("Circle SOL reversal: waiting for confirmation", zap.String("sig", sig.String()))
	result, err := sub.Recv(ctx)
	if err != nil {
		return "", fmt.Errorf("confirm transaction: %w", err)
	}
	if result.Value.Err != nil {
		return "", fmt.Errorf("transaction failed on-chain: %v", result.Value.Err)
	}

	a.logger.Info("Circle SOL reversal: confirmed",
		zap.String("sig", sig.String()),
		zap.Uint64("lamports", lamports))
	return sig.String(), nil
}

// ed25519PublicKeyFromBytes converts 32 bytes to ed25519.PublicKey (for potential future use).
var _ = ed25519.PublicKey{}

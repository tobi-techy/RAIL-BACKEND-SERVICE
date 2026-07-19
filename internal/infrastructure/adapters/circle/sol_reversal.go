package circle

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
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

	// 2. Get recent blockhash from Solana mainnet
	rpcClient := rpc.New(rpc.MainNetBeta_RPC)
	recent, err := rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("get recent blockhash: %w", err)
	}

	// 2a. Check current SOL balance — wallet must keep ~0.001 SOL for rent-exempt minimum
	bal, err := rpcClient.GetBalance(ctx, fromAddress, rpc.CommitmentFinalized)
	if err != nil {
		return "", fmt.Errorf("get SOL balance: %w", err)
	}
	const rentExemptBuffer uint64 = 1_000_000 // 0.001 SOL
	if bal.Value < lamports+rentExemptBuffer {
		return "", fmt.Errorf("insufficient balance: have %d lamports, need %d (amount + 0.001 rent buffer)", bal.Value, lamports+rentExemptBuffer)
	}
	lamports = bal.Value - rentExemptBuffer

	// 3. Build SOL transfer instruction
	ix := system.NewTransferInstruction(
		lamports,
		fromAddress,
		toAddress,
	).Build()

	tx, err := solana.NewTransaction(
		[]solana.Instruction{ix},
		recent.Value.Blockhash,
		solana.TransactionPayer(fromAddress),
	)
	if err != nil {
		return "", fmt.Errorf("build transaction: %w", err)
	}

	// Serialize as base64 for Circle SignTransaction
	rawBytes, err := tx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("serialize transaction: %w", err)
	}
	rawB64 := base64.StdEncoding.EncodeToString(rawBytes)

	a.logger.Info("Circle SOL reversal: signing transaction",
		zap.String("wallet_id", walletID),
		zap.String("from", wallet.Address),
		zap.String("to", destination),
		zap.Uint64("lamports", lamports),
		zap.Float64("sol", float64(lamports)/math.Pow(10, 9)),
		zap.Int("raw_tx_bytes", len(rawBytes)))

	// 4. Sign via Circle
	signed, err := a.client.SignTransaction(ctx, &SignTransactionRequest{
		WalletID:       walletID,
		RawTransaction: rawB64,
		Memo:           "Rail: reversal of accidental SOL deposit",
	})
	if err != nil {
		return "", fmt.Errorf("sign transaction: %w", err)
	}

	// 5. Broadcast via Solana RPC (signedTransaction is base64-encoded)
	signedBytes, err := base64.StdEncoding.DecodeString(signed.SignedTransaction)
	if err != nil {
		return "", fmt.Errorf("decode signed transaction: %w", err)
	}

	sig, err := rpcClient.SendRawTransaction(ctx, signedBytes)
	if err != nil {
		return "", fmt.Errorf("broadcast: %w", err)
	}

	// 6. Confirm
	wsClient, err := ws.Connect(ctx, rpc.MainNetBeta_WS)
	if err != nil {
		a.logger.Warn("Circle SOL reversal: could not open WS for confirmation, tx was broadcast",
			zap.String("sig", sig.String()), zap.Error(err))
		return sig.String(), nil
	}
	defer wsClient.Close()

	sub, err := wsClient.SignatureSubscribe(sig, rpc.CommitmentConfirmed)
	if err != nil {
		a.logger.Warn("Circle SOL reversal: could not subscribe to signature, tx was broadcast",
			zap.String("sig", sig.String()), zap.Error(err))
		return sig.String(), nil
	}
	defer sub.Unsubscribe()

	a.logger.Info("Circle SOL reversal: waiting for confirmation",
		zap.String("sig", sig.String()))
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

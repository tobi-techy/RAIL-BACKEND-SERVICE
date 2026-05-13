package reflect

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mr-tron/base58"
	"github.com/shopspring/decimal"
)

const (
	solanaSystemProgramID          = "11111111111111111111111111111111"
	solanaTokenProgramID           = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	solanaToken2022ProgramID       = "TokenzQdBNbLqP5VEhdkAS6EPFQj1Xz6JQyFCTYp"
	solanaAssociatedTokenProgramID = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	solanaComputeBudgetProgramID   = "ComputeBudget111111111111111111111111111111"
	solanaMemoProgramID            = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"
)

type solanaInstruction struct {
	ProgramID string
	Accounts  []int
	Data      []byte
}

type solanaMessage struct {
	RequiredSigners int
	StaticAccounts  []string
	AccountKeys     []string
	Instructions    []solanaInstruction
}

func validateReflectUserMintTransaction(rawTransaction, walletAddress string, amount decimal.Decimal, allowedProgramIDs []string) error {
	return validateReflectUserMintTransactionWithResolver(context.Background(), rawTransaction, walletAddress, amount, allowedProgramIDs, nil)
}

func (c *Client) validateReflectUserMintTransaction(ctx context.Context, rawTransaction, walletAddress string, amount decimal.Decimal, allowedProgramIDs []string) error {
	return validateReflectUserMintTransactionWithResolver(ctx, rawTransaction, walletAddress, amount, allowedProgramIDs, c.resolveLookupTableAddresses)
}

func validateReflectUserMintTransactionWithResolver(ctx context.Context, rawTransaction, walletAddress string, amount decimal.Decimal, allowedProgramIDs []string, resolver solanaLookupTableResolver) error {
	msg, err := parseSolanaTransactionWithResolver(ctx, rawTransaction, resolver)
	if err != nil {
		return err
	}
	if err := validateReflectUserTransactionEnvelope(msg, walletAddress, allowedProgramIDs); err != nil {
		return err
	}

	expectedMicroAmount := amount.Truncate(6).Shift(6).IntPart()
	seenUSDCTransfer := false
	seenReflectInstruction := false
	for _, ix := range msg.Instructions {
		switch ix.ProgramID {
		case solanaSystemProgramID:
			if len(ix.Data) > 0 {
				return fmt.Errorf("reflect transaction contains a System Program instruction")
			}
		case solanaTokenProgramID, solanaToken2022ProgramID:
			mint, transferAmount, ok, err := parseTokenTransferChecked(ix, msg.AccountKeys)
			if err != nil {
				return err
			}
			if ok {
				if mint != usdcMint {
					return fmt.Errorf("reflect mint transaction transfers unexpected SPL mint %s", mint)
				}
				if transferAmount != expectedMicroAmount {
					return fmt.Errorf("reflect mint transaction amount mismatch: got %d micro-USDC, want %d", transferAmount, expectedMicroAmount)
				}
				seenUSDCTransfer = true
			}
		default:
			if isConfiguredProgram(ix.ProgramID, allowedProgramIDs) {
				seenReflectInstruction = true
			}
		}
	}
	if !seenUSDCTransfer && !seenReflectInstruction {
		return fmt.Errorf("reflect mint transaction does not contain a checked USDC transfer or configured Reflect instruction")
	}
	return nil
}

func validateReflectUserBurnTransaction(rawTransaction, walletAddress string, allowedProgramIDs []string) error {
	return validateReflectUserBurnTransactionWithResolver(context.Background(), rawTransaction, walletAddress, allowedProgramIDs, nil)
}

func (c *Client) validateReflectUserBurnTransaction(ctx context.Context, rawTransaction, walletAddress string, allowedProgramIDs []string) error {
	return validateReflectUserBurnTransactionWithResolver(ctx, rawTransaction, walletAddress, allowedProgramIDs, c.resolveLookupTableAddresses)
}

func validateReflectUserBurnTransactionWithResolver(ctx context.Context, rawTransaction, walletAddress string, allowedProgramIDs []string, resolver solanaLookupTableResolver) error {
	msg, err := parseSolanaTransactionWithResolver(ctx, rawTransaction, resolver)
	if err != nil {
		return err
	}
	if err := validateReflectUserTransactionEnvelope(msg, walletAddress, allowedProgramIDs); err != nil {
		return err
	}
	for _, ix := range msg.Instructions {
		if ix.ProgramID == solanaSystemProgramID && len(ix.Data) > 0 {
			return fmt.Errorf("reflect transaction contains a System Program instruction")
		}
	}
	return nil
}

func validateReflectUserTransactionEnvelope(msg *solanaMessage, walletAddress string, allowedProgramIDs []string) error {
	walletAddress = strings.TrimSpace(walletAddress)
	if walletAddress == "" {
		return fmt.Errorf("wallet address is required")
	}
	if len(msg.StaticAccounts) == 0 {
		return fmt.Errorf("transaction has no static accounts")
	}
	if msg.RequiredSigners != 1 {
		return fmt.Errorf("reflect user transaction must require exactly one signer, got %d", msg.RequiredSigners)
	}
	if msg.StaticAccounts[0] != walletAddress {
		return fmt.Errorf("transaction fee payer %s does not match user yield wallet %s", msg.StaticAccounts[0], walletAddress)
	}

	allowed := defaultAllowedSolanaPrograms()
	for _, id := range allowedProgramIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			allowed[id] = struct{}{}
		}
	}
	for _, ix := range msg.Instructions {
		if _, ok := allowed[ix.ProgramID]; !ok {
			return fmt.Errorf("transaction uses unapproved Solana program %s", ix.ProgramID)
		}
	}
	return nil
}

func isConfiguredProgram(programID string, allowedProgramIDs []string) bool {
	for _, id := range allowedProgramIDs {
		if strings.TrimSpace(id) == programID {
			return true
		}
	}
	return false
}

func defaultAllowedSolanaPrograms() map[string]struct{} {
	return map[string]struct{}{
		solanaSystemProgramID:          {},
		solanaTokenProgramID:           {},
		solanaToken2022ProgramID:       {},
		solanaAssociatedTokenProgramID: {},
		solanaComputeBudgetProgramID:   {},
		solanaMemoProgramID:            {},
	}
}

type solanaLookupTableResolver func(ctx context.Context, tableAddress string) ([]string, error)

type compiledSolanaInstruction struct {
	ProgramIndex int
	Accounts     []int
	Data         []byte
}

func parseSolanaTransaction(rawTransaction string) (*solanaMessage, error) {
	return parseSolanaTransactionWithResolver(context.Background(), rawTransaction, nil)
}

func parseSolanaTransactionWithResolver(ctx context.Context, rawTransaction string, resolver solanaLookupTableResolver) (*solanaMessage, error) {
	txBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawTransaction))
	if err != nil {
		return nil, fmt.Errorf("decode Solana transaction: %w", err)
	}
	pos := 0
	sigCount, err := readCompactU16(txBytes, &pos)
	if err != nil {
		return nil, fmt.Errorf("read signature count: %w", err)
	}
	if sigCount == 0 {
		return nil, fmt.Errorf("transaction has zero signatures")
	}
	if pos+sigCount*64 > len(txBytes) {
		return nil, fmt.Errorf("transaction signature section exceeds length")
	}
	pos += sigCount * 64
	return parseSolanaMessage(ctx, txBytes[pos:], resolver)
}

func parseSolanaMessage(ctx context.Context, message []byte, resolver solanaLookupTableResolver) (*solanaMessage, error) {
	if len(message) < 3 {
		return nil, fmt.Errorf("Solana message too short")
	}
	pos := 0
	versioned := false
	if message[pos]&0x80 != 0 {
		versioned = true
		version := message[pos] & 0x7f
		if version != 0 {
			return nil, fmt.Errorf("unsupported Solana transaction version %d", version)
		}
		pos++
	}
	if pos+3 > len(message) {
		return nil, fmt.Errorf("Solana message missing header")
	}
	requiredSigners := int(message[pos])
	pos += 3

	accountCount, err := readCompactU16(message, &pos)
	if err != nil {
		return nil, fmt.Errorf("read account count: %w", err)
	}
	if accountCount == 0 {
		return nil, fmt.Errorf("Solana message has no accounts")
	}
	if pos+accountCount*32 > len(message) {
		return nil, fmt.Errorf("Solana account list exceeds message length")
	}
	staticAccounts := make([]string, accountCount)
	for i := 0; i < accountCount; i++ {
		staticAccounts[i] = base58.Encode(message[pos : pos+32])
		pos += 32
	}
	if pos+32 > len(message) {
		return nil, fmt.Errorf("Solana message missing recent blockhash")
	}
	pos += 32

	instructionCount, err := readCompactU16(message, &pos)
	if err != nil {
		return nil, fmt.Errorf("read instruction count: %w", err)
	}
	compiledInstructions := make([]compiledSolanaInstruction, 0, instructionCount)
	for i := 0; i < instructionCount; i++ {
		if pos >= len(message) {
			return nil, fmt.Errorf("instruction %d missing program index", i)
		}
		programIndex := int(message[pos])
		pos++
		accountIndexCount, err := readCompactU16(message, &pos)
		if err != nil {
			return nil, fmt.Errorf("instruction %d account indexes: %w", i, err)
		}
		if pos+accountIndexCount > len(message) {
			return nil, fmt.Errorf("instruction %d account indexes exceed message length", i)
		}
		ixAccounts := make([]int, accountIndexCount)
		for j := 0; j < accountIndexCount; j++ {
			ixAccounts[j] = int(message[pos])
			pos++
		}
		dataLen, err := readCompactU16(message, &pos)
		if err != nil {
			return nil, fmt.Errorf("instruction %d data length: %w", i, err)
		}
		if pos+dataLen > len(message) {
			return nil, fmt.Errorf("instruction %d data exceeds message length", i)
		}
		data := append([]byte(nil), message[pos:pos+dataLen]...)
		pos += dataLen
		compiledInstructions = append(compiledInstructions, compiledSolanaInstruction{
			ProgramIndex: programIndex,
			Accounts:     ixAccounts,
			Data:         data,
		})
	}

	accountKeys := append([]string(nil), staticAccounts...)
	if versioned {
		lookupCount, err := readCompactU16(message, &pos)
		if err != nil {
			return nil, fmt.Errorf("read address table lookup count: %w", err)
		}
		if lookupCount > 0 {
			if resolver == nil {
				return nil, fmt.Errorf("address table lookups require a Solana lookup table resolver")
			}
			for i := 0; i < lookupCount; i++ {
				if pos+32 > len(message) {
					return nil, fmt.Errorf("address table lookup %d missing table address", i)
				}
				tableAddress := base58.Encode(message[pos : pos+32])
				pos += 32
				tableAddresses, err := resolver(ctx, tableAddress)
				if err != nil {
					return nil, fmt.Errorf("resolve address table %s: %w", tableAddress, err)
				}
				writableIndexes, err := readLookupIndexes(message, &pos)
				if err != nil {
					return nil, fmt.Errorf("address table lookup %d writable indexes: %w", i, err)
				}
				readonlyIndexes, err := readLookupIndexes(message, &pos)
				if err != nil {
					return nil, fmt.Errorf("address table lookup %d readonly indexes: %w", i, err)
				}
				for _, index := range writableIndexes {
					if index >= len(tableAddresses) {
						return nil, fmt.Errorf("address table lookup %d writable index %d out of range", i, index)
					}
					accountKeys = append(accountKeys, tableAddresses[index])
				}
				for _, index := range readonlyIndexes {
					if index >= len(tableAddresses) {
						return nil, fmt.Errorf("address table lookup %d readonly index %d out of range", i, index)
					}
					accountKeys = append(accountKeys, tableAddresses[index])
				}
			}
		}
	} else if pos < len(message) {
		return nil, fmt.Errorf("legacy Solana message has unexpected trailing data")
	}

	if pos != len(message) {
		return nil, fmt.Errorf("Solana message has %d trailing bytes", len(message)-pos)
	}

	instructions := make([]solanaInstruction, 0, len(compiledInstructions))
	for i, ix := range compiledInstructions {
		if ix.ProgramIndex >= len(accountKeys) {
			return nil, fmt.Errorf("instruction %d program index %d outside account keys", i, ix.ProgramIndex)
		}
		for _, accountIndex := range ix.Accounts {
			if accountIndex >= len(accountKeys) {
				return nil, fmt.Errorf("instruction %d account index %d outside account keys", i, accountIndex)
			}
		}
		instructions = append(instructions, solanaInstruction{
			ProgramID: accountKeys[ix.ProgramIndex],
			Accounts:  ix.Accounts,
			Data:      ix.Data,
		})
	}

	return &solanaMessage{
		RequiredSigners: requiredSigners,
		StaticAccounts:  staticAccounts,
		AccountKeys:     accountKeys,
		Instructions:    instructions,
	}, nil
}

func readLookupIndexes(data []byte, pos *int) ([]int, error) {
	count, err := readCompactU16(data, pos)
	if err != nil {
		return nil, err
	}
	if *pos+count > len(data) {
		return nil, fmt.Errorf("lookup indexes exceed message length")
	}
	indexes := make([]int, count)
	for i := 0; i < count; i++ {
		indexes[i] = int(data[*pos])
		*pos = *pos + 1
	}
	return indexes, nil
}

func (c *Client) resolveLookupTableAddresses(ctx context.Context, tableAddress string) ([]string, error) {
	if strings.TrimSpace(c.solanaRPC) == "" {
		return nil, fmt.Errorf("solana_rpc is required")
	}
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "getAccountInfo",
		"params": []any{
			tableAddress,
			map[string]any{"encoding": "base64"},
		},
	}
	var resp struct {
		Result struct {
			Value *struct {
				Data json.RawMessage `json:"data"`
			} `json:"value"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := c.postJSON(ctx, c.solanaRPC, req, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("solana rpc error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result.Value == nil {
		return nil, fmt.Errorf("lookup table account not found")
	}
	var dataTuple []string
	if err := json.Unmarshal(resp.Result.Value.Data, &dataTuple); err != nil {
		return nil, fmt.Errorf("decode lookup table account data tuple: %w", err)
	}
	if len(dataTuple) == 0 || strings.TrimSpace(dataTuple[0]) == "" {
		return nil, fmt.Errorf("lookup table account returned empty data")
	}
	accountData, err := base64.StdEncoding.DecodeString(dataTuple[0])
	if err != nil {
		return nil, fmt.Errorf("decode lookup table account data: %w", err)
	}
	return parseLookupTableAccountAddresses(accountData)
}

func parseLookupTableAccountAddresses(data []byte) ([]string, error) {
	const lookupTableMetaSize = 56
	if len(data) < lookupTableMetaSize {
		return nil, fmt.Errorf("lookup table account data too short: %d", len(data))
	}
	addressData := data[lookupTableMetaSize:]
	if len(addressData)%32 != 0 {
		return nil, fmt.Errorf("lookup table address data length %d is not a multiple of 32", len(addressData))
	}
	addresses := make([]string, 0, len(addressData)/32)
	for len(addressData) > 0 {
		addresses = append(addresses, base58.Encode(addressData[:32]))
		addressData = addressData[32:]
	}
	return addresses, nil
}

func readCompactU16(data []byte, pos *int) (int, error) {
	var value int
	var shift uint
	for i := 0; i < 3; i++ {
		if *pos >= len(data) {
			return 0, fmt.Errorf("truncated compact-u16")
		}
		b := data[*pos]
		*pos = *pos + 1
		value |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, nil
		}
		shift += 7
	}
	return 0, fmt.Errorf("compact-u16 exceeds 3 bytes")
}

func isSystemLamportTransfer(data []byte) bool {
	return len(data) >= 4 && binary.LittleEndian.Uint32(data[:4]) == 2
}

func parseTokenTransferChecked(ix solanaInstruction, accounts []string) (string, int64, bool, error) {
	if len(ix.Data) == 0 || ix.Data[0] != 12 {
		return "", 0, false, nil
	}
	if len(ix.Data) < 10 {
		return "", 0, false, fmt.Errorf("SPL transferChecked instruction is truncated")
	}
	if len(ix.Accounts) < 4 {
		return "", 0, false, fmt.Errorf("SPL transferChecked instruction has too few accounts")
	}
	mintIndex := ix.Accounts[1]
	if mintIndex >= len(accounts) {
		return "", 0, false, fmt.Errorf("SPL transferChecked mint account index out of range")
	}
	amount := int64(binary.LittleEndian.Uint64(ix.Data[1:9]))
	return accounts[mintIndex], amount, true, nil
}

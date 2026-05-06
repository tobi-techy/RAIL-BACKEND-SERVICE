package reflect

import (
	"encoding/base64"
	"encoding/binary"
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
	Instructions    []solanaInstruction
}

func validateReflectUserMintTransaction(rawTransaction, walletAddress string, amount decimal.Decimal, allowedProgramIDs []string) error {
	msg, err := parseSolanaTransaction(rawTransaction)
	if err != nil {
		return err
	}
	if err := validateReflectUserTransactionEnvelope(msg, walletAddress, allowedProgramIDs); err != nil {
		return err
	}

	expectedMicroAmount := amount.Truncate(6).Shift(6).IntPart()
	seenUSDCTransfer := false
	for _, ix := range msg.Instructions {
		switch ix.ProgramID {
		case solanaSystemProgramID:
			if len(ix.Data) > 0 {
				return fmt.Errorf("reflect transaction contains a System Program instruction")
			}
		case solanaTokenProgramID, solanaToken2022ProgramID:
			mint, transferAmount, ok, err := parseTokenTransferChecked(ix, msg.StaticAccounts)
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
		}
	}
	if !seenUSDCTransfer {
		return fmt.Errorf("reflect mint transaction does not contain a checked USDC transfer")
	}
	return nil
}

func validateReflectUserBurnTransaction(rawTransaction, walletAddress string, allowedProgramIDs []string) error {
	msg, err := parseSolanaTransaction(rawTransaction)
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

func parseSolanaTransaction(rawTransaction string) (*solanaMessage, error) {
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
	return parseSolanaMessage(txBytes[pos:])
}

func parseSolanaMessage(message []byte) (*solanaMessage, error) {
	if len(message) < 3 {
		return nil, fmt.Errorf("Solana message too short")
	}
	pos := 0
	if message[pos]&0x80 != 0 {
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
	accounts := make([]string, accountCount)
	for i := 0; i < accountCount; i++ {
		accounts[i] = base58.Encode(message[pos : pos+32])
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
	instructions := make([]solanaInstruction, 0, instructionCount)
	for i := 0; i < instructionCount; i++ {
		if pos >= len(message) {
			return nil, fmt.Errorf("instruction %d missing program index", i)
		}
		programIndex := int(message[pos])
		pos++
		if programIndex >= len(accounts) {
			return nil, fmt.Errorf("instruction %d program index %d outside static accounts", i, programIndex)
		}
		accountIndexCount, err := readCompactU16(message, &pos)
		if err != nil {
			return nil, fmt.Errorf("instruction %d account indexes: %w", i, err)
		}
		if pos+accountIndexCount > len(message) {
			return nil, fmt.Errorf("instruction %d account indexes exceed message length", i)
		}
		ixAccounts := make([]int, accountIndexCount)
		for j := 0; j < accountIndexCount; j++ {
			accountIndex := int(message[pos])
			if accountIndex >= len(accounts) {
				return nil, fmt.Errorf("instruction %d account index %d outside static accounts", i, accountIndex)
			}
			ixAccounts[j] = accountIndex
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
		instructions = append(instructions, solanaInstruction{
			ProgramID: accounts[programIndex],
			Accounts:  ixAccounts,
			Data:      data,
		})
	}

	if pos < len(message) {
		lookupCount, err := readCompactU16(message, &pos)
		if err != nil {
			return nil, fmt.Errorf("read address table lookup count: %w", err)
		}
		if lookupCount > 0 {
			return nil, fmt.Errorf("address table lookups are not allowed in Reflect user-wallet transactions")
		}
	}

	return &solanaMessage{
		RequiredSigners: requiredSigners,
		StaticAccounts:  accounts,
		Instructions:    instructions,
	}, nil
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

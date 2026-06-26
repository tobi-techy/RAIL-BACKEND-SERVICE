package blend

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// Driving the user's Gnosis Safe without ERC-4337.
//
// Blend's withdraw action plan (deployType "multisend") contains steps that must execute
// FROM the Safe — the Safe owns the vault shares, so the owner EOA calling the vault
// directly redeems nothing. Blend's own SDK batches the steps into a single Safe MultiSend
// and submits it as an ERC-4337 UserOperation through a bundler+paymaster. We don't run a
// bundler; instead we achieve the identical on-chain effect by having the Safe's owner (the
// user's Circle Base wallet) call Safe.execTransaction directly, delegatecalling into the
// canonical MultiSend with the same packed inner transactions.
//
// Authorization uses Gnosis Safe's "pre-validated signature" (v=1): for a 1-of-1 Safe whose
// sole owner is the caller, no ECDSA signature is needed — the Safe accepts the call because
// msg.sender == owner. The Circle wallet IS the owner and IS the caller, so this holds.
//
// Failure mode is safe: any encoding/threshold/ownership mismatch makes execTransaction
// REVERT (no state change, redemption simply doesn't settle and the withdrawal reverses);
// it cannot misdirect funds, because the inner step calldata comes verbatim from Blend.

const (
	// multiSendAddress is the canonical Safe MultiSend (supports per-call delegatecall),
	// the exact address @blend-money/core uses (MULTISEND_ADDRESS).
	multiSendAddress = "0x38869bf66a61cF6bDB996A6aE40D5853Fd43B526"

	// Function selectors (first 4 bytes of keccak256 of the signature).
	selectorMultiSend       = "8d80ff0a" // multiSend(bytes)
	selectorExecTransaction = "6a761202" // execTransaction(address,uint256,bytes,uint8,uint256,uint256,uint256,address,address,bytes)
)

// encodeMultiSendData packs the steps into MultiSend's transactions blob and returns the
// full `multiSend(bytes)` calldata. Each inner transaction is
// abi.encodePacked(operation:uint8, to:address, value:uint256, dataLen:uint256, data:bytes),
// with operation = 1 (delegatecall) for liquidityReset-style steps, else 0 (call).
func encodeMultiSendData(steps []ActionStep) ([]byte, error) {
	var packed []byte
	for i, s := range steps {
		to, err := addrBytes(s.To)
		if err != nil {
			return nil, fmt.Errorf("multisend step %d: %w", i, err)
		}
		data, err := hexBytes(s.Data)
		if err != nil {
			return nil, fmt.Errorf("multisend step %d data: %w", i, err)
		}
		value, err := weiValue(s.Value)
		if err != nil {
			return nil, fmt.Errorf("multisend step %d value: %w", i, err)
		}
		op := byte(0)
		if s.IsDelegateCall {
			op = 1
		}
		valueWord, err := leftPad32(value.Bytes())
		if err != nil {
			return nil, fmt.Errorf("multisend step %d value: %w", i, err)
		}
		packed = append(packed, op)
		packed = append(packed, to...)        // 20 bytes
		packed = append(packed, valueWord...) // 32 bytes
		packed = append(packed, uint256Bytes(uint64(len(data)))...)
		packed = append(packed, data...)
	}
	// multiSend(bytes): selector + abi.encode(bytes) [offset, length, padded data].
	out := mustHex(selectorMultiSend)
	out = append(out, uint256Bytes(32)...)                  // offset to bytes
	out = append(out, uint256Bytes(uint64(len(packed)))...) // length
	out = append(out, rightPad32(packed)...)                // data, right-padded
	return out, nil
}

// encodeSafeExecTransaction builds Safe.execTransaction calldata that delegatecalls into
// MultiSend (operation=1) with the given multiSend calldata, authorized by a pre-validated
// signature for ownerEOA. value/safeTxGas/baseGas/gasPrice are 0; gasToken/refundReceiver
// are the zero address.
func encodeSafeExecTransaction(multiSendCalldata []byte, ownerEOA string) ([]byte, error) {
	to, err := addrBytes(multiSendAddress)
	if err != nil {
		return nil, err
	}
	toWord, err := leftPad32(to)
	if err != nil {
		return nil, err
	}
	sig, err := preValidatedSignature(ownerEOA)
	if err != nil {
		return nil, err
	}

	const headWords = 10
	headLen := headWords * 32
	dataOffset := headLen
	sigOffset := headLen + 32 + len(rightPad32(multiSendCalldata))

	out := mustHex(selectorExecTransaction)
	out = append(out, toWord...)                           // 1: to = MultiSend
	out = append(out, uint256Bytes(0)...)                  // 2: value
	out = append(out, uint256Bytes(uint64(dataOffset))...) // 3: data offset
	out = append(out, uint256Bytes(1)...)                  // 4: operation = 1 (delegatecall)
	out = append(out, uint256Bytes(0)...)                  // 5: safeTxGas
	out = append(out, uint256Bytes(0)...)                  // 6: baseGas
	out = append(out, uint256Bytes(0)...)                  // 7: gasPrice
	out = append(out, make([]byte, 32)...)                 // 8: gasToken = 0x0
	out = append(out, make([]byte, 32)...)                 // 9: refundReceiver = 0x0
	out = append(out, uint256Bytes(uint64(sigOffset))...)  // 10: signatures offset
	// tail: data
	out = append(out, uint256Bytes(uint64(len(multiSendCalldata)))...)
	out = append(out, rightPad32(multiSendCalldata)...)
	// tail: signatures
	out = append(out, uint256Bytes(uint64(len(sig)))...)
	out = append(out, rightPad32(sig)...)
	return out, nil
}

// preValidatedSignature returns the 65-byte Gnosis Safe pre-validated signature for owner:
// r = left-padded owner address, s = 0, v = 1. Valid only when msg.sender == owner and the
// Safe threshold is 1.
func preValidatedSignature(ownerEOA string) ([]byte, error) {
	to, err := addrBytes(ownerEOA)
	if err != nil {
		return nil, fmt.Errorf("owner address: %w", err)
	}
	r, err := leftPad32(to)
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 0, 65)
	sig = append(sig, r...)                // r = owner (left-padded)
	sig = append(sig, make([]byte, 32)...) // s = 0
	sig = append(sig, byte(1))             // v = 1
	return sig, nil
}

// --- ABI / hex helpers ---

func addrBytes(addr string) ([]byte, error) {
	a := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(addr)), "0x")
	if len(a) != 40 {
		return nil, fmt.Errorf("invalid address %q", addr)
	}
	b, err := hex.DecodeString(a)
	if err != nil {
		return nil, fmt.Errorf("invalid address hex %q: %w", addr, err)
	}
	return b, nil
}

func hexBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return []byte{}, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid hex %q: %w", s, err)
	}
	return b, nil
}

func weiValue(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0x" {
		return big.NewInt(0), nil
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, ok := new(big.Int).SetString(s[2:], 16)
		if !ok {
			return nil, fmt.Errorf("invalid hex value %q", s)
		}
		return v, nil
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("invalid decimal value %q", s)
	}
	return v, nil
}

// uint256Bytes returns a 32-byte big-endian encoding of n. n is a uint64, so it always
// fits a word — no overflow path, no error.
func uint256Bytes(n uint64) []byte {
	out := make([]byte, 32)
	binary.BigEndian.PutUint64(out[24:], n)
	return out
}

// leftPad32 left-pads b into a 32-byte word. It returns an error rather than panicking
// when b exceeds 32 bytes, so a malformed value can never crash transaction encoding on
// the money path.
func leftPad32(b []byte) ([]byte, error) {
	if len(b) > 32 {
		return nil, fmt.Errorf("value %x exceeds 32 bytes", b)
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out, nil
}

// rightPad32 right-pads b to a multiple of 32 bytes (for dynamic bytes payloads).
func rightPad32(b []byte) []byte {
	if len(b)%32 == 0 {
		return b
	}
	out := make([]byte, ((len(b)/32)+1)*32)
	copy(out, b)
	return out
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic("blend: bad constant hex " + s)
	}
	return b
}

func hexEncode(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}

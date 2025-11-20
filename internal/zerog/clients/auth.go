package clients

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// RequestSigner handles cryptographic signing for 0G requests
type RequestSigner struct {
	privateKey *ecdsa.PrivateKey
	address    common.Address
}

// NewRequestSigner creates a new request signer from a private key
func NewRequestSigner(privateKeyHex string) (*RequestSigner, error) {
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("error casting public key to ECDSA")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	return &RequestSigner{
		privateKey: privateKey,
		address:    address,
	}, nil
}

// SignRequest signs a request with timestamp and nonce
func (s *RequestSigner) SignRequest(payload string, nonce int64) (string, error) {
	timestamp := time.Now().Unix()
	message := fmt.Sprintf("%d:%d:%s", timestamp, nonce, payload)
	
	hash := crypto.Keccak256Hash([]byte(message))
	signature, err := crypto.Sign(hash.Bytes(), s.privateKey)
	if err != nil {
		return "", fmt.Errorf("signing failed: %w", err)
	}

	return hexutil.Encode(signature), nil
}

// GetAddress returns the Ethereum address
func (s *RequestSigner) GetAddress() string {
	return s.address.Hex()
}

// GenerateNonce generates a unique nonce
func GenerateNonce() int64 {
	return time.Now().UnixNano()
}

// VerifySignature verifies a signature (for testing)
func VerifySignature(message, signatureHex, addressHex string) (bool, error) {
	hash := crypto.Keccak256Hash([]byte(message))
	
	signature, err := hexutil.Decode(signatureHex)
	if err != nil {
		return false, err
	}

	if len(signature) != 65 {
		return false, fmt.Errorf("invalid signature length")
	}

	if signature[64] >= 27 {
		signature[64] -= 27
	}

	pubKey, err := crypto.SigToPub(hash.Bytes(), signature)
	if err != nil {
		return false, err
	}

	recoveredAddr := crypto.PubkeyToAddress(*pubKey)
	expectedAddr := common.HexToAddress(addressHex)

	return recoveredAddr == expectedAddr, nil
}

// RecoverAddress recovers the address from a signature
func RecoverAddress(message, signatureHex string) (string, error) {
	hash := crypto.Keccak256Hash([]byte(message))
	
	signature, err := hexutil.Decode(signatureHex)
	if err != nil {
		return "", err
	}

	if len(signature) != 65 {
		return "", fmt.Errorf("invalid signature length")
	}

	if signature[64] >= 27 {
		signature[64] -= 27
	}

	pubKey, err := crypto.SigToPub(hash.Bytes(), signature)
	if err != nil {
		return "", err
	}

	address := crypto.PubkeyToAddress(*pubKey)
	return address.Hex(), nil
}

// SignTypedData signs EIP-712 typed data
func (s *RequestSigner) SignTypedData(domainHash, messageHash []byte) ([]byte, error) {
	rawData := append([]byte("\x19\x01"), append(domainHash, messageHash...)...)
	hash := crypto.Keccak256Hash(rawData)
	
	signature, err := crypto.Sign(hash.Bytes(), s.privateKey)
	if err != nil {
		return nil, fmt.Errorf("signing typed data failed: %w", err)
	}

	signature[64] += 27
	return signature, nil
}

// ParseBigInt safely parses a big integer
func ParseBigInt(s string) (*big.Int, error) {
	n := new(big.Int)
	n, ok := n.SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("invalid big int: %s", s)
	}
	return n, nil
}

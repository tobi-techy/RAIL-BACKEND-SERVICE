# 0G Compute Network - Production Implementation Guide

## 📋 Executive Summary

Based on the official 0G documentation, your current implementation is **partially complete** but missing critical production features. This guide provides the exact steps to make your 0G integration production-ready.

## 🔍 Current State Analysis

### ✅ What You Have
1. **Storage Integration** - Fully implemented with 0G SDK
2. **Basic Inference Gateway** - HTTP-based inference requests
3. **Namespace Management** - Content-addressable storage
4. **Artifact Storage** - Storing AI outputs in 0G

### ❌ What's Missing (CRITICAL)

#### 1. **0G Broker Integration** (HIGHEST PRIORITY)
Your current implementation bypasses the 0G broker entirely. The broker is **mandatory** for:
- Authentication and billing
- Provider discovery and selection
- Request signing and verification
- Automatic settlement

#### 2. **Proper Authentication**
- No Ethereum wallet integration
- No request signing with private keys
- No provider acknowledgment

#### 3. **Account Management**
- No prepaid account funding
- No balance checking
- No automatic settlement

## 🎯 Production Implementation Plan

### Phase 1: Broker Integration (Week 1)

#### Step 1.1: Add Go Ethereum Dependencies

```go
// go.mod additions needed
require (
    github.com/ethereum/go-ethereum v1.13.0
    github.com/0glabs/0g-serving-broker-go v0.1.0 // If available
)
```

#### Step 1.2: Create Broker Client

```go
// internal/infrastructure/zerog/broker_client.go
package zerog

import (
    "context"
    "crypto/ecdsa"
    "fmt"
    
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/crypto"
    "github.com/ethereum/go-ethereum/ethclient"
    "go.uber.org/zap"
)

type BrokerClient struct {
    ethClient   *ethclient.Client
    privateKey  *ecdsa.PrivateKey
    address     common.Address
    rpcURL      string
    logger      *zap.Logger
}

func NewBrokerClient(rpcURL, privateKeyHex string, logger *zap.Logger) (*BrokerClient, error) {
    // Connect to 0G chain
    client, err := ethclient.Dial(rpcURL)
    if err != nil {
        return nil, fmt.Errorf("failed to connect to 0G chain: %w", err)
    }
    
    // Load private key
    privateKey, err := crypto.HexToECDSA(privateKeyHex)
    if err != nil {
        return nil, fmt.Errorf("invalid private key: %w", err)
    }
    
    address := crypto.PubkeyToAddress(privateKey.PublicKey)
    
    return &BrokerClient{
        ethClient:  client,
        privateKey: privateKey,
        address:    address,
        rpcURL:     rpcURL,
        logger:     logger,
    }, nil
}
```

#### Step 1.3: Implement Service Discovery

```go
// Service represents a 0G compute provider
type Service struct {
    Provider      common.Address
    ServiceType   string
    URL           string
    InputPrice    *big.Int
    OutputPrice   *big.Int
    Model         string
    Verifiability string
}

func (b *BrokerClient) ListServices(ctx context.Context) ([]Service, error) {
    // Call smart contract to list available services
    // This requires the broker contract ABI
    return nil, fmt.Errorf("not implemented")
}

func (b *BrokerClient) GetServiceMetadata(ctx context.Context, providerAddr common.Address) (*ServiceMetadata, error) {
    // Get service endpoint and model info
    return nil, fmt.Errorf("not implemented")
}
```

#### Step 1.4: Implement Request Signing

```go
func (b *BrokerClient) GenerateRequestHeaders(ctx context.Context, providerAddr common.Address, messageContent string) (map[string]string, error) {
    // Generate nonce
    nonce := generateNonce()
    
    // Create signature payload
    payload := fmt.Sprintf("%s:%s:%d", providerAddr.Hex(), messageContent, nonce)
    
    // Sign with private key
    hash := crypto.Keccak256Hash([]byte(payload))
    signature, err := crypto.Sign(hash.Bytes(), b.privateKey)
    if err != nil {
        return nil, fmt.Errorf("failed to sign request: %w", err)
    }
    
    return map[string]string{
        "X-0G-Address":   b.address.Hex(),
        "X-0G-Signature": hexutil.Encode(signature),
        "X-0G-Nonce":     fmt.Sprintf("%d", nonce),
    }, nil
}
```

### Phase 2: Account Management (Week 1-2)

#### Step 2.1: Implement Ledger Management

```go
type LedgerManager struct {
    broker *BrokerClient
    logger *zap.Logger
}

func (l *LedgerManager) AddFunds(ctx context.Context, amount *big.Int) error {
    // Call smart contract to deposit funds
    // This requires the ledger contract ABI
    return fmt.Errorf("not implemented")
}

func (l *LedgerManager) GetBalance(ctx context.Context) (*big.Int, error) {
    // Query balance from smart contract
    return nil, fmt.Errorf("not implemented")
}

func (l *LedgerManager) WithdrawFunds(ctx context.Context, serviceType string) error {
    // Withdraw unused funds
    return fmt.Errorf("not implemented")
}
```

### Phase 3: Enhanced Inference Gateway (Week 2)

#### Step 3.1: Update Inference Gateway

```go
// internal/infrastructure/zerog/inference_gateway.go
type InferenceGateway struct {
    broker           *BrokerClient
    ledger           *LedgerManager
    storageClient    entities.ZeroGStorageClient
    logger           *zap.Logger
    
    // Provider cache
    providers        map[string]*Service
    selectedProvider common.Address
}

func (g *InferenceGateway) GenerateWeeklySummary(ctx context.Context, request *entities.WeeklySummaryRequest) (*entities.InferenceResult, error) {
    // 1. Select provider
    provider, err := g.selectProvider(ctx, "weekly_summary")
    if err != nil {
        return nil, err
    }
    
    // 2. Get service metadata
    metadata, err := g.broker.GetServiceMetadata(ctx, provider.Provider)
    if err != nil {
        return nil, err
    }
    
    // 3. Build prompt
    prompt := g.buildWeeklySummaryPrompt(request)
    
    // 4. Generate auth headers
    headers, err := g.broker.GenerateRequestHeaders(ctx, provider.Provider, prompt)
    if err != nil {
        return nil, err
    }
    
    // 5. Make request to provider
    result, err := g.makeProviderRequest(ctx, metadata.Endpoint, prompt, headers)
    if err != nil {
        return nil, err
    }
    
    // 6. Verify response (if verifiable)
    if provider.Verifiability == "TeeML" {
        valid, err := g.verifyResponse(ctx, provider.Provider, result)
        if err != nil || !valid {
            return nil, fmt.Errorf("response verification failed")
        }
    }
    
    return result, nil
}
```

#### Step 3.2: Implement Provider Selection

```go
func (g *InferenceGateway) selectProvider(ctx context.Context, taskType string) (*Service, error) {
    // Get all available services
    services, err := g.broker.ListServices(ctx)
    if err != nil {
        return nil, err
    }
    
    // Filter by task requirements
    var candidates []*Service
    for _, svc := range services {
        if g.isProviderSuitable(svc, taskType) {
            candidates = append(candidates, &svc)
        }
    }
    
    if len(candidates) == 0 {
        return nil, fmt.Errorf("no suitable providers found")
    }
    
    // Select based on strategy (cost, latency, reliability)
    return g.selectBestProvider(candidates), nil
}

func (g *InferenceGateway) isProviderSuitable(svc Service, taskType string) bool {
    // Check if provider supports required model
    // Check if provider is online
    // Check if we have sufficient balance
    return true
}

func (g *InferenceGateway) selectBestProvider(candidates []*Service) *Service {
    // Strategy: Select cheapest provider
    // TODO: Implement more sophisticated selection (latency, reliability)
    cheapest := candidates[0]
    for _, candidate := range candidates[1:] {
        totalCost := new(big.Int).Add(candidate.InputPrice, candidate.OutputPrice)
        cheapestCost := new(big.Int).Add(cheapest.InputPrice, cheapest.OutputPrice)
        if totalCost.Cmp(cheapestCost) < 0 {
            cheapest = candidate
        }
    }
    return cheapest
}
```

### Phase 4: Configuration Updates (Week 2)

#### Step 4.1: Update Configuration

```yaml
# configs/config.yaml
zerog:
  # 0G Chain RPC
  chain:
    rpc_url: "https://evmrpc-testnet.0g.ai"  # Testnet
    # rpc_url: "https://evmrpc.0g.ai"  # Mainnet
    chain_id: 16600  # Testnet
    # chain_id: 16600  # Mainnet
  
  # Broker Configuration
  broker:
    private_key: "${ZEROG_PRIVATE_KEY}"  # Ethereum private key
    auto_fund: true
    min_balance: "1.0"  # Minimum balance in 0G tokens
    topup_amount: "10.0"  # Auto-topup amount
  
  # Provider Selection
  providers:
    strategy: "cost"  # cost, latency, reliability
    official_only: true  # Use only official 0G providers
    fallback_enabled: true
    
  # Official Providers (from docs)
  official_providers:
    gpt-oss-120b: "0xf07240Efa67755B5311bc75784a061eDB47165Dd"
    deepseek-r1-70b: "0x3feE5a4dd5FDb8a32dDA97Bed899830605dBD9D3"
  
  # Storage (existing)
  storage:
    rpc_endpoint: "https://rpc-storage-testnet.0g.ai"
    indexer_rpc: "https://indexer-storage-testnet.0g.ai"
    private_key: "${ZEROG_STORAGE_PRIVATE_KEY}"
```

#### Step 4.2: Update Config Struct

```go
// internal/infrastructure/config/config.go
type ZeroGConfig struct {
    Chain    ZeroGChainConfig    `mapstructure:"chain"`
    Broker   ZeroGBrokerConfig   `mapstructure:"broker"`
    Storage  ZeroGStorageConfig  `mapstructure:"storage"`
    Providers ProviderConfig     `mapstructure:"providers"`
}

type ZeroGChainConfig struct {
    RPCURL  string `mapstructure:"rpc_url"`
    ChainID int64  `mapstructure:"chain_id"`
}

type ZeroGBrokerConfig struct {
    PrivateKey  string  `mapstructure:"private_key"`
    AutoFund    bool    `mapstructure:"auto_fund"`
    MinBalance  string  `mapstructure:"min_balance"`
    TopupAmount string  `mapstructure:"topup_amount"`
}

type ProviderConfig struct {
    Strategy        string            `mapstructure:"strategy"`
    OfficialOnly    bool              `mapstructure:"official_only"`
    FallbackEnabled bool              `mapstructure:"fallback_enabled"`
    OfficialProviders map[string]string `mapstructure:"official_providers"`
}
```

## 🔧 Implementation Checklist

### Week 1: Core Integration
- [ ] Add Ethereum dependencies
- [ ] Create BrokerClient
- [ ] Implement service discovery
- [ ] Implement request signing
- [ ] Add provider acknowledgment
- [ ] Implement ledger management
- [ ] Add balance checking

### Week 2: Enhanced Features
- [ ] Update InferenceGateway
- [ ] Implement provider selection
- [ ] Add response verification
- [ ] Implement auto-funding
- [ ] Add provider failover
- [ ] Update configuration
- [ ] Add comprehensive logging

### Week 3: Testing & Optimization
- [ ] Integration tests with testnet
- [ ] Load testing
- [ ] Cost optimization
- [ ] Latency optimization
- [ ] Error handling improvements
- [ ] Documentation

## 📊 Key Differences from Current Implementation

| Feature | Current | Production Required |
|---------|---------|-------------------|
| **Authentication** | Generic HTTP headers | Ethereum wallet signing |
| **Provider Discovery** | Hardcoded endpoint | Smart contract query |
| **Request Headers** | Static | Dynamic, signed per request |
| **Account Management** | None | Prepaid ledger with auto-topup |
| **Provider Selection** | Single endpoint | Multi-provider with selection |
| **Verification** | None | TEE verification for verifiable services |
| **Settlement** | None | Automatic on-chain settlement |
| **Failover** | None | Automatic provider failover |

## 🚀 Quick Start (Minimal Changes)

If you need to get started quickly, here's the absolute minimum:

### 1. Add Broker Wrapper

```go
// internal/infrastructure/zerog/broker_wrapper.go
package zerog

// Minimal broker implementation using HTTP directly
type MinimalBroker struct {
    privateKey string
    address    string
    rpcURL     string
}

func (b *MinimalBroker) GetOfficialProvider(model string) string {
    providers := map[string]string{
        "gpt-oss-120b":   "0xf07240Efa67755B5311bc75784a061eDB47165Dd",
        "deepseek-r1-70b": "0x3feE5a4dd5FDb8a32dDA97Bed899830605dBD9D3",
    }
    return providers[model]
}
```

### 2. Update Inference Gateway

```go
// Use official provider addresses
providerAddr := "0xf07240Efa67755B5311bc75784a061eDB47165Dd"
endpoint := "https://provider-endpoint.0g.ai"  // Get from service discovery

// Add proper headers
headers := map[string]string{
    "Content-Type": "application/json",
    "X-0G-Provider": providerAddr,
}
```

## 📚 Resources

- **Official Docs**: https://docs.0g.ai/developer-hub/building-on-0g/compute-network/sdk
- **GitHub Examples**: https://github.com/0gfoundation/0g-compute-ts-starter-kit
- **Discord Support**: https://discord.gg/0glabs
- **Testnet RPC**: https://evmrpc-testnet.0g.ai
- **Mainnet RPC**: https://evmrpc.0g.ai

## ⚠️ Critical Notes

1. **OpenAI SDK Compatible**: 0G providers use OpenAI-compatible API format
2. **Single-Use Headers**: Request headers can only be used once
3. **Provider Acknowledgment**: Must acknowledge provider before first use
4. **Prepaid Model**: Must fund account before making requests
5. **TEE Verification**: Verifiable services require response verification
6. **Automatic Settlement**: Broker handles settlement automatically

## 🎯 Success Criteria

Your implementation is production-ready when:

- ✅ Using 0G broker for all requests
- ✅ Proper Ethereum wallet authentication
- ✅ Automatic provider discovery and selection
- ✅ Prepaid account with auto-topup
- ✅ Response verification for verifiable services
- ✅ Provider failover implemented
- ✅ Comprehensive error handling
- ✅ Cost and latency monitoring
- ✅ Integration tests passing on testnet
- ✅ Documentation complete

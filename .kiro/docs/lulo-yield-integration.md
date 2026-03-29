# Lulo Yield Integration — Pool-Level Architecture

## Architecture

```
User Deposit → Allocation (70/30) → stash_balance (ledger)
                                          |
                                  Treasury Sweep Worker (every 10 min)
                                          |
                              ┌───────────┴───────────┐
                              │ Phase 1: Bridge→Solana │
                              │ CreateTransfer (USDC)  │
                              │ Poll until settled     │
                              └───────────┬───────────┘
                                          |
                              ┌───────────┴───────────┐
                              │ Phase 2: Solana→Lulo   │
                              │ Generate tx → sign →   │
                              │ submit to Solana RPC   │
                              └───────────┬───────────┘
                                          |
                                  Lulo DeFi Protocols
                                  (Kamino, Drift, etc.)
                                          |
                              Yield Distribution Worker (monthly)
                                          |
                              CreditStash per user (ledger)
```

## Lulo API (on-chain Solana program)

| Endpoint | Method | Purpose |
|---|---|---|
| `/v1/generate.transactions.deposit` | POST | Returns serialized Solana tx for depositing USDC |
| `/v1/generate.transactions.withdraw` | POST | Returns serialized Solana tx for withdrawing USDC |
| `/v1/account.getAccount?owner=<pubkey>` | GET | Account state (totalValue, interestEarned, depositedValue) |
| `/v1/pool.getPools` | GET | Pool APY, price, liquidity |
| `/v1/rates.getRates` | GET | Current/historical yield rates |

Key: Lulo is non-custodial. Funds go directly into underlying protocols. The API generates transactions — Rail signs and submits them.

## Two-Phase Sweep

The sweep worker bridges the gap between Bridge custody (where user USDC lives) and Lulo (which needs USDC in a Solana wallet):

1. **Phase 1**: Bridge `CreateTransfer` moves USDC from custody wallet → Rail's Solana wallet. Polls `GetTransfer` until `payment_processed`.
2. **Phase 2**: Lulo `generate.transactions.deposit` → sign with ed25519 → submit to Solana RPC.

Withdrawals are single-phase (Lulo→Solana wallet). Moving USDC back to Bridge is a separate concern.

## Files

| File | Purpose |
|---|---|
| `config.go` | `LuloConfig`: APIKey, BaseURL, SolanaRPC, OwnerWallet, PrivateKey, PoolType, MinSweepAmount, SweepInterval, BridgeSourceWalletID |
| `adapters/lulo/client.go` | Solana tx signing client: Deposit, Withdraw, GetAccount, GetPools, GetRates |
| `workers/treasury_sweep/worker.go` | Two-phase sweep: Bridge→Solana→Lulo |
| `di/lulo_adapters.go` | Rewards adapter (delta via DB high-water mark), reconciliation adapter |
| `workers/yield_distribution/worker.go` | Advances high-water mark after successful distribution |
| `migrations/132_treasury_positions.up.sql` | `treasury_positions` + `yield_state` tables |

## Configuration

```yaml
lulo:
  api_key: "xxx"
  base_url: "https://api.lulo.fi"
  solana_rpc: "https://api.mainnet-beta.solana.com"
  owner_wallet: "<base58 pubkey>"
  private_key: "<base58 64-byte ed25519 key>"
  pool_type: "protected"
  min_sweep_amount: "100"
  sweep_interval: 10
  bridge_source_wallet_id: "<Bridge wallet ID holding USDC on Solana>"
```

## What Did NOT Change

Allocation, auto-invest, station, ledger, yield service (RunDistribution, computeTWB) — completely unchanged.

# Solana vault bootstrap (Stocklana hackathon)

Filled `configs/vault/{steady,balanced,growth}.yaml` with Glider-validated Solana CAIP-19s
(Backed xStocks + Solana stables). Tenant seed strategies were created on Glider so
`make seed-catalog` can ingest them.

## After merge / on a box with the Glider key

```bash
export INVESTMENT_GLIDER_ENABLED=true
export INVESTMENT_GLIDER_API_KEY=…
make seed-catalog CONFIRM=1
bash scripts/classify-solana-vault-legs.sh
export VAULT_SETTLEMENT_ACCOUNT='solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp:<settlement-pubkey>'
# restart → BootstrapStrategies
# prove: GET /api/v1/vault/strategies
```

## Allocations

| Tier | Mix |
|------|-----|
| Steady | USDC 40 / USDT 25 / PYUSD 20 / USDS 15 (all yield) |
| Balanced | SPYx 25 / QQQx 15 / NVDAx 15 / AAPLx 10 / TSLAx 10 / USDC 25 |
| Growth | NVDAx 20 / TSLAx 15 / SPYx 20 / QQQx 15 / METAx 10 / GLDx 10 / USDC 10 |

Weights sum to 100. No leg exceeds 40.

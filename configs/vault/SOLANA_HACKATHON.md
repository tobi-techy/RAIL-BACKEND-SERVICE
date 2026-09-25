# Solana vault bootstrap (Stocklana hackathon)

Filled `configs/vault/{steady,balanced,growth}.yaml` with Glider-validated Solana CAIP-19s
(Backed xStocks + Solana stables). Tenant seed strategies were created on Glider so
`make seed-catalog` can ingest them.

Expanded 2026-09-25: added MSFTx / GOOGLx / AMZNx to Balanced and Growth; rebalanced
Steady cash sleeve. All books re-validated via `POST /v2/strategies/validate` (HTTP 200).

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
| Steady | USDC 30 / USDT 25 / PYUSD 25 / USDS 20 (all yield) |
| Balanced | SPYx 20 / QQQx 12 / NVDAx 10 / MSFTx 10 / GOOGLx 8 / AAPLx 8 / AMZNx 7 / TSLAx 5 / USDC 20 |
| Growth | NVDAx 15 / TSLAx 12 / SPYx 15 / QQQx 12 / METAx 8 / MSFTx 8 / GOOGLx 8 / AMZNx 7 / GLDx 10 / USDC 5 |

Weights sum to 100. No leg exceeds 40.

## Inventory (Glider-accepted Solana CAIPs)

| Symbol | Class | Mint |
|--------|-------|------|
| USDC | yield | `EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v` |
| USDT | yield | `Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB` |
| PYUSD | yield | `2b1kV6DkPAnxd5ixfnxCpjxmKwqjjaYmCZfHsFu24GXo` |
| USDS | yield | `USDSwr9ApdHk5bvJKMjzff41FfuX8bSxdKcR81vTwcA` |
| AAPLx | equity | `XsbEhLAtcf6HdfpFZ5xEMdqW8nfAvcsP5bdudRLJzJp` |
| NVDAx | equity | `Xsc9qvGR1efVDFGLrVsmkzv3qi45LTBjeUKSPmx9qEh` |
| TSLAx | equity | `XsDoVfqeBukxuZHWhdvWHBhgEHjGNst4MLodqsJHzoB` |
| MSFTx | equity | `XspzcW1PRtgf6Wj92HCiZdjzKCyFekVD8P5Ueh3dRMX` |
| GOOGLx | equity | `XsCPL9dNWBMvFtTmwcCA5v3xWPSMEBCszbQdiLLq6aN` |
| AMZNx | equity | `Xs3eBt7uRfJX8QUs4suhyU8p2M6DoUDrJyWBa8LLZsg` |
| METAx | equity | `Xsa62P5mvPszXL1krVUnU5ar38bBSVcWAB6fmPCo5Zu` |
| SPYx | equity | `XsoCS1TfEyfFhfvj8EtZ528L3CaKBDBRqRapnBbDF2W` |
| QQQx | equity | `Xs8S1uUs1zvS2p7iwtsG3b6fkhpvmwz4GYU3gWAmWHZ` |
| GLDx | gold | `Xsv9hRk1z5ystj9MhnA7Lq4vjSsLwzL2nxrwmwtD3re` |

Prefix: `solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:`

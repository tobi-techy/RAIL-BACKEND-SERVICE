#!/usr/bin/env bash
# Run from RAIL-BACKEND-SERVICE after: make seed-catalog CONFIRM=1
# Requires INVESTMENT_GLIDER_API_KEY in env.
# Classifies Solana vault legs used by configs/vault/{steady,balanced,growth}.yaml
set -euo pipefail

echo "classify USDC → yield"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v CLASS=yield APPLY=1
echo "classify USDT → yield"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB CLASS=yield APPLY=1
echo "classify PYUSD → yield"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:2b1kV6DkPAnxd5ixfnxCpjxmKwqjjaYmCZfHsFu24GXo CLASS=yield APPLY=1
echo "classify USDS → yield"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:USDSwr9ApdHk5bvJKMjzff41FfuX8bSxdKcR81vTwcA CLASS=yield APPLY=1
echo "classify SPYx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XsoCS1TfEyfFhfvj8EtZ528L3CaKBDBRqRapnBbDF2W CLASS=equity APPLY=1
echo "classify QQQx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:Xs8S1uUs1zvS2p7iwtsG3b6fkhpvmwz4GYU3gWAmWHZ CLASS=equity APPLY=1
echo "classify NVDAx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:Xsc9qvGR1efVDFGLrVsmkzv3qi45LTBjeUKSPmx9qEh CLASS=equity APPLY=1
echo "classify AAPLx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XsbEhLAtcf6HdfpFZ5xEMdqW8nfAvcsP5bdudRLJzJp CLASS=equity APPLY=1
echo "classify TSLAx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XsDoVfqeBukxuZHWhdvWHBhgEHjGNst4MLodqsJHzoB CLASS=equity APPLY=1
echo "classify MSFTx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XspzcW1PRtgf6Wj92HCiZdjzKCyFekVD8P5Ueh3dRMX CLASS=equity APPLY=1
echo "classify GOOGLx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:XsCPL9dNWBMvFtTmwcCA5v3xWPSMEBCszbQdiLLq6aN CLASS=equity APPLY=1
echo "classify AMZNx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:Xs3eBt7uRfJX8QUs4suhyU8p2M6DoUDrJyWBa8LLZsg CLASS=equity APPLY=1
echo "classify METAx → equity"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:Xsa62P5mvPszXL1krVUnU5ar38bBSVcWAB6fmPCo5Zu CLASS=equity APPLY=1
echo "classify GLDx → gold"
make classify ID=solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp/spl:Xsv9hRk1z5ystj9MhnA7Lq4vjSsLwzL2nxrwmwtD3re CLASS=gold APPLY=1
echo "Done. Set VAULT_SETTLEMENT_ACCOUNT and restart Rail so BootstrapStrategies picks up configs/vault/*.yaml"

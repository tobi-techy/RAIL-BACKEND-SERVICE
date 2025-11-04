## Epic 2: Stablecoin Funding Flow
**Summary:** Enable users to fund their brokerage accounts instantly with stablecoins using Due for off-ramp/on-ramp functionality.

**In-Scope Features:**
- Support deposits from Ethereum (EVM) and Solana (non-EVM) into the user's Circle wallet.
- **NEW:** Create virtual accounts linked to Alpaca brokerage accounts for each user.
- **NEW:** Orchestrate an immediate **USDC-to-USD off-ramp** via **Due API**.
- **NEW:** Securely transfer the resulting USD into the user's linked Alpaca brokerage account.
- **NEW:** Handle the reverse flow: **USD (Alpaca) -> USDC (Due)** for withdrawals.
- **NEW:** Integrate KYC verification via **Sumsub** for compliance.
- **NEW:** Implement recipient management for withdrawal destinations.

**Success Criteria:**
- Users can fund their brokerage account within minutes of a confirmed stablecoin deposit.
- At least 2 supported deposit pathways at launch.
- Virtual accounts successfully linked to Alpaca accounts.
- KYC verification completed via Sumsub integration.
- End-to-end funding/withdrawal success rate of >99%.
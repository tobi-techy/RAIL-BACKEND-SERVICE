# Technical Considerations

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Success Metrics](./03-success-metrics.md)
- **Next:** [Risks & Open Questions](./05-risks.md)
- **[Index](./README.md)**

---

## Technical Considerations

### Target Platforms
- Native mobile applications for **iOS and Android**.

### Technology Stack
- **Frontend:** React Native (cross-platform mobile framework).
- **Backend:** **Go**
- **Database:** PostgreSQL.

### Infrastructure & Integrations
- **0G** for storage and AI capabilities.
- **Circle** for **Developer-Controlled Wallets** (custody only - no off-ramp/on-ramp).
- **Due** for **USDC <-> USD on/off-ramps**, virtual accounts, and recipient management.
- **Sumsub** for KYC verification and compliance.
- **Alpaca** for brokerage, trade execution, and custody of traditional assets.

### Architecture Strategy
- Initial approach: **Modular Monolith** (Go services) within a monorepo for faster MVP delivery.
- Long-term: Designed to evolve into a more distributed architecture as user base scales.

### Constraints
- **Timeline:** 3-week deadline for MVP cycle.
- **Dependencies:** Reliance on 0G, Circle, and Alpaca APIs.

### Assumptions
- Regulatory compliance model is viable.
- Third-party APIs (0G, Circle, Alpaca) are stable and cost-effective at scale.

---

**Next:** [Risks & Open Questions](./05-risks.md)

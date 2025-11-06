# Risks & Open Questions

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Technical Considerations](./04-technical-considerations.md)
- **Next:** [MVP Epics](./06-epics.md)
- **[Index](./README.md)**

---

## Risks & Open Questions

### Key Risks

#### 1. Regulatory Risk
* Hybrid Web3 + traditional model may face compliance challenges in multiple jurisdictions.
* Potential impact on stablecoin usage, custody models, and cross-border flows.

#### 2. Execution Risk
* Tight 3-week MVP timeline creates potential for scope creep or missed deadlines.
* Reliance on multiple third-party APIs increases fragility.

#### 3. Technical Risk (APIs)
* Performance and reliability of 0G, Circle, and Alpaca APIs are critical to user experience.
* Unknown at-scale costs of API usage.

#### 4. Market Risk (User Trust)
* Gen Z skepticism toward both banks and crypto means adoption may be slower than expected.
* Building trust will require careful onboarding and consistent reliability.

---

### Open Questions
- **RESOLVED:** Which brokerage partner will be selected? (Answer: Alpaca)
- **RESOLVED:** What wallet management solution? (Answer: Circle Developer-Controlled Wallets)
- Has a full legal/regulatory review been conducted for the *specific* USDC -> Alpaca flow?
- What are the true at-scale costs of Circle's off-ramp + Alpaca's funding APIs?
- How will fraud prevention and compliance (KYC/AML) be handled in this multi-partner flow?

---

**Next:** [MVP Epics](./06-epics.md)

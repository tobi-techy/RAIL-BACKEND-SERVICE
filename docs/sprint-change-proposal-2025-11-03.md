# Sprint Change Proposal: Replace Circle with Due for Off-Ramp/On-Ramp

**Date:** 2025-11-03
**Prepared By:** Product Manager (John)
**Change Scope:** Moderate - Requires documentation updates and new API integration

## Issue Summary

Technical constraints with Circle's off-ramp/on-ramp functionality have been identified, requiring replacement with Due API (https://due.readme.io/reference) while maintaining Circle for multi-chain wallet custody. Additional requirements include virtual accounts linked to Alpaca, KYC integration with Sumsub, and recipient management features.

## Impact Analysis

### Epic Impact
- **Epic 2 (Stablecoin Funding Flow)**: Complete revision required to replace Circle off-ramp/on-ramp with Due
- **Other Epics**: No impact - Circle wallet functionality preserved

### Artifact Conflicts
- **PRD**: Major conflicts in functional requirements and technical considerations
- **Architecture**: Major conflicts in data flows, component diagrams, and tech stack
- **UI/UX**: No conflicts identified (no UI specs available)
- **Code**: No impact (Epic 2 not yet implemented)

## Recommended Approach

**Selected Path:** Direct Adjustment
**Rationale:** Epic 2 remains in backlog with no implementation started, allowing clean replacement of Circle with Due for off-ramp/on-ramp while preserving Circle for wallet custody. Minimizes risk and maintains MVP timeline.

**Effort Estimate:** Medium (documentation updates + new API integration)
**Risk Level:** Low (no code changes required)
**Timeline Impact:** None (Epic 2 not started)

## Detailed Change Proposals

### 1. Epic Updates (docs/epics.md)
**Epic 2 Scope Revision:**
- Replace Circle off-ramp/on-ramp with Due API
- Add virtual accounts linked to Alpaca
- Add KYC integration with Sumsub
- Add recipient management
- Update success criteria

### 2. PRD Updates (docs/prd.md)
**Functional Requirements:**
- Update funding/withdrawal flows to use Due instead of Circle
- Add virtual account and KYC requirements
- Add recipient management features

**Technical Considerations:**
- Replace Circle off-ramp/on-ramp with Due
- Add Sumsub for KYC/AML
- Maintain Circle for wallet custody only

### 3. Architecture Updates (docs/architecture.md)
**System Overview:**
- Update data flows to show Due for off-ramp/on-ramp
- Add Sumsub for KYC integration
- Separate Circle (wallets) from Due (funding)

**Component Changes:**
- Funding Service: Replace Circle with Due API
- Add virtual account management
- Update sequence diagrams for funding/withdrawal flows

**Data Models:**
- Add virtual_accounts table for Due integration

## Implementation Handoff

**Scope Classification:** Moderate
**Handoff Recipients:** Development Team (for API integration), Scrum Master (for backlog updates)

**Responsibilities:**
- **Development Team**: Implement Due API integration, virtual accounts, Sumsub KYC
- **Scrum Master**: Update sprint-status.yaml to reflect Epic 2 changes
- **Product Manager**: Oversee integration and testing

**Success Criteria:**
- Due API successfully integrated for off-ramp/on-ramp
- Virtual accounts created and linked to Alpaca
- Sumsub KYC verification working
- Circle wallet functionality preserved
- All change proposals implemented in documentation

#!/bin/bash

# Rail MVP - Linear Issues Creation Script
# This script generates Linear CLI commands to create all missing MVP implementation issues

echo "🚀 Creating Linear Issues for Rail MVP Missing Implementations"
echo "============================================================="

# Set your Linear team ID (replace with actual team ID)
TEAM_ID="your-team-id"

# Create Epics first
echo ""
echo "📋 Creating Epics..."

echo "linear epic create --title 'MVP Critical Path - Core Automation Features' --description 'Critical features required for MVP launch including automatic splitting, investment engine, and debit card system.' --target-date '2025-01-26'"

echo "linear epic create --title 'Post-MVP Feature Enhancements' --description 'Important features to ship shortly after MVP including physical cards and copy trading.' --target-date '2025-04-15'"

echo "linear epic create --title 'Future Platform Improvements' --description 'Long-term platform improvements for scalability and user experience.' --target-date '2025-07-15'"

# Create P0 Issues (Critical MVP Blockers)
echo ""
echo "🔥 Creating P0 Issues (Critical MVP Blockers)..."

echo "linear issue create \\
  --title 'Implement Apple Sign-In Authentication' \\
  --description 'Implement Apple Sign-In as the primary authentication method for iOS users. The social auth framework exists but Apple-specific implementation is missing.

Requirements:
- Apple Sign-In SDK integration
- Apple ID token validation server-side
- User profile creation from Apple ID
- Fallback to email/phone for non-Apple users

Acceptance Criteria:
- [ ] Users can sign up using Apple Sign-In
- [ ] Apple ID tokens are validated server-side
- [ ] User accounts created automatically from Apple profile
- [ ] Seamless onboarding flow completes in < 2 minutes

Technical Notes:
Extend existing social auth service in internal/domain/services/socialauth/. Add Apple provider configuration to config.go. Implement Apple ID token verification. Update auth handlers to support Apple Sign-In flow.' \\
  --priority 1 \\
  --estimate 5 \\
  --label mvp \\
  --label authentication \\
  --label p0 \\
  --label ios"

echo "linear issue create \\
  --title 'Implement Automatic 70/30 Deposit Split Engine' \\
  --description 'Implement the core system rule where every deposit is automatically split 70% to Spend Balance and 30% to Invest Balance without any user interaction.

Requirements:
- Deposit webhook triggers automatic split
- 70% credited to Spend Balance
- 30% credited to Invest Balance
- Real-time balance updates
- No user configuration options

Acceptance Criteria:
- [ ] All deposits automatically split 70/30
- [ ] Split occurs within 60 seconds of deposit confirmation
- [ ] No user settings or allocation choices shown
- [ ] Balance updates reflect split immediately
- [ ] Works for both crypto and fiat deposits

Technical Notes:
Enhance funding webhook handlers in internal/workers/funding_webhook/. Integrate with allocation service in internal/domain/services/allocation/. Update balance service for real-time updates. Add split logic to deposit confirmation flow.' \\
  --priority 1 \\
  --estimate 8 \\
  --label mvp \\
  --label funding \\
  --label automation \\
  --label p0"

echo "linear issue create \\
  --title 'Implement Virtual Debit Card System' \\
  --description 'Implement virtual debit card issuance and management system. Cards should be issued immediately upon first funding and linked directly to Spend Balance.

Requirements:
- Virtual card issuance API integration
- Real-time authorization against Spend Balance
- Card details (number, CVV, expiry) generation
- Transaction processing and balance deduction

Acceptance Criteria:
- [ ] Virtual card issued on first funding
- [ ] Card transactions deduct from Spend Balance in real-time
- [ ] Authorization fails when insufficient balance
- [ ] Card details accessible via API
- [ ] Transaction history tracked

Technical Notes:
Research and integrate card issuer API (Stripe Issuing, Marqeta, etc.). Create card service in internal/domain/services/card/. Add card handlers in internal/api/handlers/. Integrate with balance service for real-time authorization. Add card entities and repositories.' \\
  --priority 1 \\
  --estimate 13 \\
  --label mvp \\
  --label debit-card \\
  --label payments \\
  --label p0"

echo "linear issue create \\
  --title 'Complete Automatic Investment Engine Implementation' \\
  --description 'Complete the automatic investment engine to deploy 30% invest balance without user interaction. Alpaca integration exists but auto-deployment logic is missing.

Requirements:
- Auto-deployment of invest balance funds
- Global fallback investment strategy
- Integration with existing Alpaca service
- No trade confirmations or asset visibility
- Position tracking (backend only)

Acceptance Criteria:
- [ ] 30% of deposits auto-invested without user action
- [ ] Default investment strategy defined and implemented
- [ ] No individual trades or assets shown to users
- [ ] Positions tracked in backend systems
- [ ] Investment triggers on balance threshold

Technical Notes:
Enhance investing service in internal/domain/services/investing/. Define global fallback strategy (e.g., diversified ETF portfolio). Add auto-investment triggers to allocation service. Integrate with existing Alpaca service. Ensure trades are invisible to users in API responses.' \\
  --priority 1 \\
  --estimate 10 \\
  --label mvp \\
  --label investing \\
  --label automation \\
  --label p0"

echo "linear issue create \\
  --title 'Setup USD/GBP Virtual Accounts via Due Network' \\
  --description 'Setup dedicated virtual accounts for USD and GBP bank transfers using the existing Due Network integration.

Requirements:
- USD virtual account creation via Due Network
- GBP virtual account creation via Due Network
- Bank transfer routing and processing
- Deposit confirmation and crediting

Acceptance Criteria:
- [ ] Users receive unique USD virtual account details
- [ ] Users receive unique GBP virtual account details
- [ ] Bank transfers credited within standard timeframes
- [ ] Automatic 70/30 split triggered on fiat deposits
- [ ] Virtual account details accessible via API

Technical Notes:
Extend Due Network adapter in internal/adapters/due/. Add fiat virtual account creation to onboarding flow. Update funding handlers to support fiat deposits. Ensure fiat deposits trigger automatic split. Add virtual account entities and repositories.' \\
  --priority 1 \\
  --estimate 8 \\
  --label mvp \\
  --label funding \\
  --label fiat \\
  --label p0"

# Create P1 Issues (Important Features)
echo ""
echo "⚡ Creating P1 Issues (Important Features)..."

echo "linear issue create \\
  --title 'Implement Physical Debit Card System' \\
  --description 'Implement physical card ordering, shipping, and management system for users who request physical cards.

Requirements:
- Physical card ordering system
- Shipping address management
- Card activation flow
- Link to existing virtual card system

Acceptance Criteria:
- [ ] Users can order physical cards
- [ ] Shipping address collection and validation
- [ ] Card activation via app
- [ ] Physical card linked to same Spend Balance as virtual card

Dependencies: Virtual Debit Card System' \\
  --priority 2 \\
  --estimate 8 \\
  --label p1 \\
  --label debit-card \\
  --label physical-card"

echo "linear issue create \\
  --title 'Optimize KYC Flow for Sub-2-Minute Onboarding' \\
  --description 'Optimize existing KYC flow to achieve the < 2 minute onboarding target while maintaining compliance.

Requirements:
- Streamlined document upload
- Faster verification processing
- Reduced friction points
- Progress indicators

Acceptance Criteria:
- [ ] KYC completion time reduced to < 2 minutes
- [ ] Document upload optimized for mobile
- [ ] Real-time progress feedback
- [ ] Maintain compliance standards' \\
  --priority 2 \\
  --estimate 5 \\
  --label p1 \\
  --label kyc \\
  --label onboarding \\
  --label optimization"

echo "linear issue create \\
  --title 'Implement Copy Trading System (Conductors)' \\
  --description 'Implement the copy trading system allowing users to follow professional investors (Conductors) and mirror their portfolios.

Requirements:
- Conductor application system
- Track creation and management
- Copy engine for trade mirroring
- Discovery and follow system

Acceptance Criteria:
- [ ] Conductor application and approval workflow
- [ ] Track creation with asset allocation
- [ ] Automatic trade mirroring for followers
- [ ] Track discovery and follow functionality

Dependencies: Automatic Investment Engine' \\
  --priority 3 \\
  --estimate 21 \\
  --label p1 \\
  --label copy-trading \\
  --label post-mvp"

# Create P2 Issues (Future Improvements)
echo ""
echo "🔮 Creating P2 Issues (Future Improvements)..."

echo "linear issue create \\
  --title 'Enhance Portfolio Analytics Beyond Basic Tracking' \\
  --description 'Implement advanced portfolio analytics beyond basic balance tracking for better user insights.

Requirements:
- Performance metrics calculation
- Risk analysis
- Historical tracking
- Reporting dashboards' \\
  --priority 4 \\
  --estimate 13 \\
  --label p2 \\
  --label analytics \\
  --label future"

echo "linear issue create \\
  --title 'Optimize APIs for Mobile App Performance' \\
  --description 'Create mobile-optimized API endpoints for better app performance and user experience.

Requirements:
- Batch API endpoints
- Reduced payload sizes
- Offline capability support
- Push notification integration' \\
  --priority 4 \\
  --estimate 8 \\
  --label p2 \\
  --label mobile \\
  --label api-optimization"

echo ""
echo "✅ Linear issue creation commands generated!"
echo ""
echo "📝 Next Steps:"
echo "1. Replace 'your-team-id' with your actual Linear team ID"
echo "2. Install Linear CLI: npm install -g @linear/cli"
echo "3. Authenticate: linear auth"
echo "4. Run the commands above to create all issues"
echo "5. Assign issues to team members and set due dates"
echo ""
echo "📊 Summary:"
echo "- 5 P0 Issues (Critical MVP Blockers) - 44 story points"
echo "- 3 P1 Issues (Important Features) - 34 story points" 
echo "- 2 P2 Issues (Future Improvements) - 21 story points"
echo "- Total: 10 issues, 99 story points"
echo ""
echo "🎯 MVP Target: Complete P0 issues within 5-7 weeks"

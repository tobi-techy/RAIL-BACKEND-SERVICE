# RAIL Backend API - Bruno Collection

This Bruno collection contains all API endpoints for testing the RAIL backend according to the MVP epics and PRD.

## Setup

1. Install [Bruno](https://www.usebruno.com/) (open-source API client)
2. Open Bruno and select "Open Collection"
3. Navigate to this `bruno` folder
4. Select the `local` environment

## Environment Variables

Configure in `environments/local.bru`:

| Variable | Description |
|----------|-------------|
| `baseUrl` | API base URL (default: http://localhost:8080) |
| `apiVersion` | API version (default: v1) |
| `accessToken` | JWT access token (auto-set after login) |
| `refreshToken` | JWT refresh token (auto-set after login) |
| `userId` | Current user ID (auto-set after login) |
| `csrfToken` | CSRF token for protected endpoints |

## Collection Structure (by Epic)

### Epic 1: User Onboarding & Authentication (`Auth/`, `Onboarding/`)
- Register, Login, Social Login (Apple Sign-In)
- Start Onboarding, Get Status, Submit KYC
- **Goal**: Download to funded account in < 2 minutes

### Epic 2: Funding & Deposits (`Funding/`)
- Create Virtual Account (USD/GBP)
- Create Deposit Address (Crypto - ETH, Polygon, BSC, Solana)
- Get Balances, Transaction History
- **Goal**: Deposit → Split → Update in < 60 seconds

### Epic 3: Spend Balance & Ledger (`Funding/`)
- Get Balances (real-time, 99.9% accuracy)
- Get Station (home screen)
- **Goal**: Checking account replacement

### Epic 4: Debit Card (`Cards/`)
- Create Virtual Card, Get Cards
- Freeze/Unfreeze Card
- Get Transactions
- **Goal**: Card usable immediately after funding

### Epic 5: Automatic Investing Engine (`Portfolio/`, `Allocation/`)
- Enable 70/30 Allocation Mode
- List Baskets, Invest in Basket
- Get Portfolio Overview
- **Goal**: 30% auto-deployed without user interaction

### Epic 6: Round-Ups Automation (`Roundups/`)
- Get/Update Round-Up Settings
- **Goal**: Simple ON/OFF toggle

### Epic 7: Home Screen (`Funding/Get Station.bru`)
- Total balance, Spend/Invest split, System status
- **Goal**: Answer "Is my money working?" at a glance

### Epic 8: Conductors - Copy Trading (`CopyTrading/`, `Admin/`)
- Apply as Conductor, Create Track
- List Conductors/Tracks, Follow/Unfollow
- Admin: Review Applications
- **Goal**: One-tap follow with automatic trade mirroring

## Testing Flow

### 1. Basic User Flow
```
1. Auth/Register or Auth/Social Login
2. Onboarding/Start Onboarding
3. Onboarding/Submit KYC
4. Funding/Create Virtual Account
5. Funding/Get Balances
6. Allocation/Enable Mode
7. Funding/Get Station
```

### 2. Card Flow
```
1. Login
2. Cards/Create Card
3. Cards/Get Cards
4. Cards/Get Transactions
```

### 3. Copy Trading Flow
```
1. Login
2. CopyTrading/List Conductors
3. CopyTrading/List Tracks
4. CopyTrading/Follow Conductor
5. CopyTrading/List My Drafts
6. CopyTrading/Unfollow
```

### 4. Become a Conductor Flow
```
1. Login (existing user)
2. CopyTrading/Apply as Conductor
3. Admin/List Pending Applications (admin)
4. Admin/Review Conductor Application (admin)
5. CopyTrading/Create Track
```

## Core System Rule

**Every deposit is automatically split 70/30:**
- 70% → Spend Balance (everyday expenses)
- 30% → Invest Engine (automatic allocation)

This is system-defined and always on in MVP.

## Notes

- All protected endpoints require Bearer token authentication
- CSRF token is required for state-changing operations
- Tokens are automatically saved to environment after login
- Use `local` environment for development testing

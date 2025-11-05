# Data Models

**Version:** v0.2  
**Last Updated:** October 24, 2025

## Navigation
- **Previous:** [Tech Stack](./01-tech-stack.md)
- **Next:** [Components](./03-components.md)
- **[Index](./README.md)**

---

## 4. Data Models

Based on the original architecture and the PRD, these are the core data entities required for the MVP. This section defines their purpose, key attributes, and relationships.

### 4.1 users
* **Purpose:** Represents an end-user of the STACK application.
* **Key Attributes:**
    * `id`: UUID (Primary Key)
    * `auth_provider_id`: String (e.g., Auth0/Cognito subject claim, unique)
    * `email`: String (Indexed, unique)
    * `phone_number`: String (Optional, Indexed, unique)
    * `kyc_status`: Enum (`not_started`, `pending`, `approved`, `rejected`)
    * `passcode_hash`: String (Hashed passcode for app login)
    * `created_at`: Timestamp
    * `updated_at`: Timestamp
* **Relationships:**
    * One-to-Many with `wallets`
    * One-to-Many with `deposits`
    * One-to-One with `balances`
    * One-to-Many with `orders`
    * One-to-Many with `positions`
    * One-to-One with `portfolio_perf` (potentially, or track history)
    * One-to-Many with `ai_summaries`

### 4.2 wallets
* **Purpose:** Stores metadata about Circle Developer-Controlled Wallets associated with a user.
* **Key Attributes:**
    * `id`: UUID (Primary Key)
    * `user_id`: UUID (Foreign Key to `users.id`)
    * `chain`: Enum (`ethereum`, `solana`, etc.)
    * `address`: String (Chain-specific address, Indexed)
    * `circle_wallet_id`: String (Reference to Circle's wallet ID)
    * `status`: Enum (`creating`, `active`, `inactive`)
    * `created_at`: Timestamp
    * `updated_at`: Timestamp
* **Relationships:**
    * Many-to-One with `users`

### 4.3 virtual_accounts
* **Purpose:** Stores metadata about Due virtual accounts linked to user Alpaca brokerage accounts.
* **Key Attributes:**
    * `id`: UUID (Primary Key)
    * `user_id`: UUID (Foreign Key to `users.id`)
    * `due_virtual_account_id`: String (Reference to Due's virtual account ID)
    * `alpaca_account_id`: String (Linked Alpaca account ID)
    * `status`: Enum (`creating`, `active`, `inactive`)
    * `created_at`: Timestamp
    * `updated_at`: Timestamp
* **Relationships:**
    * Many-to-One with `users`

### 4.4 deposits
* **Purpose:** Tracks incoming stablecoin deposits from the blockchain into the Circle wallet.
* **Key Attributes:**
    * `id`: UUID (Primary Key)
    * `user_id`: UUID (Foreign Key to `users.id`)
    * `wallet_id`: UUID (Foreign Key to `wallets.id`)
    * `chain`: Enum (`ethereum`, `solana`, etc.)
    * `tx_hash`: String (Blockchain transaction hash, Indexed, unique per chain)
    * `token`: Enum (`USDC`)
    * `amount_stablecoin`: Decimal (Amount deposited in USDC)
    * `status`: Enum (`pending_confirmation`, `confirmed_on_chain`, `off_ramp_initiated`, `off_ramp_complete`, `broker_funded`, `failed`)
    * `confirmed_at`: Timestamp (When confirmed on-chain)
    * `off_ramp_completed_at`: Timestamp (When Circle confirms USD conversion)
    * `broker_funded_at`: Timestamp (When Alpaca confirms USD deposit)
    * `created_at`: Timestamp
    * `updated_at`: Timestamp
* **Relationships:**
    * Many-to-One with `users`
    * Many-to-One with `wallets`

### 4.5 balances
* **Purpose:** Represents the user's *brokerage* buying power (in USD) available at Alpaca. Circle balances are managed via Circle API.
* **Key Attributes:**
    * `user_id`: UUID (Primary Key, Foreign Key to `users.id`)
    * `buying_power_usd`: Decimal (Amount available for trading at Alpaca)
    * `pending_broker_deposits_usd`: Decimal (Amount in flight from Circle off-ramp to Alpaca)
    * `updated_at`: Timestamp
* **Relationships:**
    * One-to-One with `users`

### 4.6 baskets
* **Purpose:** Stores the definition of curated investment baskets.
* **Key Attributes:**
    * `id`: UUID (Primary Key)
    * `name`: String
    * `description`: String
    * `risk_level`: Enum (`conservative`, `balanced`, `growth`)
    * `composition_json`: JSONB (Stores array of {symbol, weight})
    * `is_active`: Boolean
    * `created_at`: Timestamp
    * `updated_at`: Timestamp
* **Relationships:**
    * One-to-Many with `orders`
    * One-to-Many with `positions` (indirectly via orders/brokerage data)

### 4.7 orders
* **Purpose:** Tracks user requests to buy or sell assets (baskets or options) via Alpaca.
* **Key Attributes:**
    * `id`: UUID (Primary Key)
    * `user_id`: UUID (Foreign Key to `users.id`)
    * `basket_id`: UUID (Optional, Foreign Key to `baskets.id`, null for options)
    * `asset_type`: Enum (`basket`, `option`)
    * `option_details_json`: JSONB (Optional, stores option contract specifics if `asset_type` is `option`)
    * `side`: Enum (`buy`, `sell`)
    * `amount_usd`: Decimal (Target order amount in USD)
    * `status`: Enum (`pending`, `accepted_by_broker`, `partially_filled`, `filled`, `failed`, `canceled`)
    * `Alpaca_order_ref`: String (Reference from Alpaca API, Indexed)
    * `created_at`: Timestamp
    * `updated_at`: Timestamp
* **Relationships:**
    * Many-to-One with `users`
    * Many-to-One with `baskets` (nullable)

### 4.8 positions (Derived/Cached)
* **Purpose:** Represents the user's current holdings at Alpaca. This might be primarily fetched from Alpaca and cached locally, rather than being the system of record.
* **Key Attributes:**
    * `user_id`: UUID (Foreign Key to `users.id`)
    * `symbol`: String (e.g., VTI, or option contract symbol)
    * `quantity`: Decimal
    * `average_price`: Decimal
    * `asset_type`: Enum (`stock`, `etf`, `option`)
    * `last_updated_at`: Timestamp (Cache timestamp)
* **Relationships:**
    * Many-to-One with `users`

### 4.9 ai_summaries
* **Purpose:** Stores the generated AI CFO summaries.
* **Key Attributes:**
    * `id`: UUID (Primary Key)
    * `user_id`: UUID (Foreign Key to `users.id`)
    * `week_start_date`: Date
    * `summary_markdown`: Text
    * `generated_at`: Timestamp
* **Relationships:**
    * Many-to-One with `users`

*(Note: `portfolio_perf` and `audit_logs` from the original doc are important but might be deferred slightly post-MVP core functionality or derived differently).*

---

**Next:** [Components](./03-components.md)

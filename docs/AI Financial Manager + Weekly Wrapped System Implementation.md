# AI Financial Manager \+ Weekly Wrapped System Implementation
## Problem Statement
Implement a comprehensive AI\-powered Financial Manager system that provides:
1. Weekly Spotify\-Wrapped\-style portfolio summaries
2. Personalized market news feed
3. Basket recommendations with previews
4. Interactive chat experience for portfolio queries
5. Backend data aggregation and AI orchestration
## Current State Overview
### Existing Infrastructure
**AI\-CFO Service \(Basic \- TO BE REPLACED\):**
* `AICfoService` exists in `internal/domain/services/aicfo_service.go` \(will be refactored\)
* Currently generates weekly summaries via 0G inference \(DEPRECATED\)
* Stores summaries in `ai_summaries` table \(will continue to use\)
* Endpoints: `GET /api/v1/ai/summary/latest`, `POST /api/v1/ai/analyze` \(will continue to use\)
* **NEW**: Replace 0G with OpenAI/Gemini free tier API
* **NEW**: Direct API calls instead of 0G storage layer
**Database Schema:**
* `users` \- User profiles
* `deposits` \- USDC deposits with status tracking
* `withdrawals` \- Withdrawal requests
* `orders` \- Investment orders \(buy/sell\)
* `positions` \- User holdings in baskets
* `baskets` \- Curated investment portfolios
* `balances` \- User buying power
* `portfolio_perf` \- Historical NAV/PnL tracking
* `ai_summaries` \- Weekly AI summaries \(week\_start, summary\_md, artifact\_uri\)
**Missing Data Structures:**
* Round\-up/cashback tracking \(not in schema\)
* Investment streak tracking
* News storage
* Chat history
* Basket recommendations
* Rebalance previews
**Existing Services:**
* `PortfolioService` \- Basic rebalancing calculations
* `FundingService` \- Deposit handling
* `InvestingService` \- Order management
* `BalanceService` \- Balance queries
* `NotificationService` \- User notifications
**Integration Points:**
* Alpaca API for brokerage operations
* Circle Developer Wallets for USDC
* Due for off\-ramp/on\-ramp
* **OpenAI API** \(free tier\) for AI inference \- primary
* **Google Gemini API** \(free tier\) for AI inference \- fallback
## Proposed Architecture
### 1\. New AI Infrastructure
**A\. AI Provider Abstraction Layer \(New\)**
* Location: `internal/infrastructure/ai/provider.go`
* Interface defining common operations \(chat completions, tool calling\)
* Implementations:
    * `OpenAIProvider` \- Primary provider using OpenAI API
    * `GeminiProvider` \- Fallback provider using Google Gemini API
* Features:
    * Automatic retry with exponential backoff
    * Provider failover \(OpenAI → Gemini\)
    * Rate limit handling
    * Token counting and cost tracking
* Configuration:
    * API keys from environment variables
    * Model selection \(gpt\-3\.5\-turbo, gemini\-1\.5\-flash\)
    * Max tokens, temperature, timeout settings
**B\. Weekly Summary Aggregator \(New\)**
* Location: `internal/domain/services/summary/aggregator.go`
* Responsibilities:
    * Aggregate weekly portfolio data
    * Calculate performance metrics
    * Track user activities \(deposits, orders, contributions\)
    * Prepare structured data for AI
* Triggered: CRON job every Sunday at midnight \(user timezone\)
**B\. News Service \(New\)**
* Location: `internal/domain/services/news/service.go`
* Responsibilities:
    * Fetch news from external API \(Finnhub/Alpaca News\)
    * Filter by user portfolio holdings
    * Store relevant articles
    * Generate personalized feed
* External API: Alpaca News API or Finnhub
**D\. AI Orchestrator Service \(Enhanced\)**
* Location: `internal/domain/services/ai/orchestrator.go`
* Responsibilities:
    * Enhanced prompt templates for Wrapped cards
    * Tool definitions mapping to backend APIs
    * Chat context management
    * Safety guardrails \(no financial advice\)
    * Response formatting for card UI
    * Provider selection and failover logic
    * Function/tool calling orchestration
* Dependencies:
    * `AIProvider` interface \(OpenAI/Gemini\)
    * Portfolio/Activity/News services for tool execution
    * Prompt template manager
    * Response parser and validator
**E\. Basket Recommendation Engine \(New\)**
* Location: `internal/domain/services/baskets/recommender.go`
* Responsibilities:
    * Analyze user portfolio vs available baskets
    * Generate rebalancing suggestions
    * Create preview orders
    * Calculate expected outcomes
### 2\. Required APIs
**Portfolio Endpoints \(New/Enhanced\):**
```warp-runnable-command
GET  /api/v1/portfolio/weekly-stats?week_start=YYYY-MM-DD
GET  /api/v1/portfolio/allocations
GET  /api/v1/portfolio/top-movers?period=1w
GET  /api/v1/portfolio/performance?period=1w|1m|3m|1y
```
**Activity Endpoints \(New\):**
```warp-runnable-command
GET  /api/v1/activity/contributions?type=deposits|roundups|cashback
GET  /api/v1/activity/streak
GET  /api/v1/activity/timeline?start=DATE&end=DATE
```
**News Endpoints \(New\):**
```warp-runnable-command
GET  /api/v1/news/feed?limit=10
GET  /api/v1/news/weekly
POST /api/v1/news/mark-read
```
**Basket Endpoints \(New/Enhanced\):**
```warp-runnable-command
GET  /api/v1/baskets/recommended
POST /api/v1/baskets/preview-rebalance
POST /api/v1/baskets/execute-rebalance
```
**AI Chat Endpoints \(New\):**
```warp-runnable-command
POST /api/v1/ai/chat
GET  /api/v1/ai/chat/history?limit=50
DELETE /api/v1/ai/chat/history
```
**Wrapped Endpoints \(New\):**
```warp-runnable-command
GET  /api/v1/wrapped/latest
GET  /api/v1/wrapped/history?limit=10
GET  /api/v1/wrapped/:id
POST /api/v1/wrapped/generate  (admin/cron)
```
### 3\. AI Provider Configuration
**OpenAI Setup:**
```go
// internal/infrastructure/ai/openai_provider.go
type OpenAIProvider struct {
    apiKey      string
    model       string // gpt-3.5-turbo or gpt-4
    maxTokens   int
    temperature float64
    timeout     time.Duration
    client      *http.Client
}
func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
func (p *OpenAIProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error)
```
**Gemini Setup:**
```go
// internal/infrastructure/ai/gemini_provider.go
type GeminiProvider struct {
    apiKey      string
    model       string // gemini-1.5-flash or gemini-1.5-pro
    maxTokens   int
    temperature float64
    timeout     time.Duration
    client      *http.Client
}
func (p *GeminiProvider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
func (p *GeminiProvider) ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error)
```
**Provider Interface:**
```go
// internal/infrastructure/ai/provider.go
type AIProvider interface {
    ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    ChatCompletionWithTools(ctx context.Context, req *ChatRequest, tools []Tool) (*ChatResponse, error)
    Name() string
    IsAvailable(ctx context.Context) bool
}
type ChatRequest struct {
    Messages    []Message
    SystemPrompt string
    MaxTokens   int
    Temperature float64
}
type ChatResponse struct {
    Content     string
    ToolCalls   []ToolCall
    TokensUsed  int
    Provider    string
    FinishReason string
}
type Tool struct {
    Name        string
    Description string
    Parameters  map[string]interface{}
}
type ToolCall struct {
    ID       string
    Name     string
    Arguments map[string]interface{}
}
```
### 4\. Database Schema Changes
**New Tables:**
```SQL
-- User activity tracking (round-ups, cashback)
CREATE TABLE user_contributions (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  type VARCHAR(20) CHECK (type IN ('deposit', 'roundup', 'cashback', 'referral')),
  amount DECIMAL(36,18),
  source VARCHAR(100),
  created_at TIMESTAMP
);
-- Investment streak tracking
CREATE TABLE investment_streaks (
  user_id UUID PRIMARY KEY REFERENCES users(id),
  current_streak INT DEFAULT 0,
  longest_streak INT DEFAULT 0,
  last_investment_date DATE,
  updated_at TIMESTAMP
);
-- Personalized news storage
CREATE TABLE user_news (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  source VARCHAR(50),
  title TEXT,
  summary TEXT,
  url TEXT,
  related_symbols TEXT[], -- Array of tickers
  published_at TIMESTAMP,
  is_read BOOLEAN DEFAULT false,
  relevance_score DECIMAL(3,2),
  created_at TIMESTAMP
);
-- AI chat history
CREATE TABLE ai_chat_sessions (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  started_at TIMESTAMP,
  last_message_at TIMESTAMP
);
CREATE TABLE ai_chat_messages (
  id UUID PRIMARY KEY,
  session_id UUID REFERENCES ai_chat_sessions(id),
  role VARCHAR(10) CHECK (role IN ('user', 'assistant')),
  content TEXT,
  metadata JSONB,
  created_at TIMESTAMP
);
-- Basket recommendations
CREATE TABLE basket_recommendations (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  recommended_basket_id UUID REFERENCES baskets(id),
  reason TEXT,
  expected_return DECIMAL(5,2),
  risk_change VARCHAR(20),
  confidence_score DECIMAL(3,2),
  is_applied BOOLEAN DEFAULT false,
  created_at TIMESTAMP,
  expires_at TIMESTAMP
);
-- Rebalance previews
CREATE TABLE rebalance_previews (
  id UUID PRIMARY KEY,
  user_id UUID REFERENCES users(id),
  target_allocation JSONB, -- {basket_id: weight}
  trades_preview JSONB, -- [{symbol, action, quantity, price}]
  expected_fees DECIMAL(36,18),
  expected_tax_impact DECIMAL(36,18),
  status VARCHAR(20) DEFAULT 'pending',
  created_at TIMESTAMP,
  expires_at TIMESTAMP
);
-- Enhanced ai_summaries (if not already has these fields)
ALTER TABLE ai_summaries 
  ADD COLUMN summary_type VARCHAR(20) DEFAULT 'weekly',
  ADD COLUMN cards_json JSONB,  -- Structured card data
  ADD COLUMN insights_json JSONB;
```
### 4\. AI Agent Design
**System Prompt Template:**
```warp-runnable-command
You are the STACK Financial Manager - a friendly, Gen Z-native AI assistant.
Behavior Rules:
- Speak in short, punchy Spotify-Wrapped style
- Use emojis sparingly (1-2 per message max)
- Never invent numbers - only use data from tools
- NEVER give financial advice (no "you should buy/sell")
- Instead say "you might consider" or "some investors..."
- Format output as structured cards for mobile UI
- Keep responses under 200 words unless detailed analysis requested
```
**Available Tools \(Function Calling\):**
```json
{
  "tools": [
    {
      "name": "get_portfolio_stats",
      "description": "Get current portfolio statistics",
      "parameters": {"period": "string"}
    },
    {
      "name": "get_top_movers",
      "description": "Get biggest gainers/losers",
      "parameters": {"limit": "integer"}
    },
    {
      "name": "get_allocations",
      "description": "Current portfolio allocation by basket"
    },
    {
      "name": "get_contributions",
      "description": "User activity (deposits, round-ups, cashback)",
      "parameters": {"type": "string", "period": "string"}
    },
    {
      "name": "get_weekly_news",
      "description": "Relevant news for user holdings"
    },
    {
      "name": "get_basket_recommendations",
      "description": "Suggested baskets based on portfolio"
    },
    {
      "name": "preview_rebalance",
      "description": "Preview rebalancing to target allocation",
      "parameters": {"target_allocation": "object"}
    },
    {
      "name": "get_risk_profile",
      "description": "User risk tolerance and preferences"
    }
  ]
}
```
### 5\. Weekly Wrapped Generation Flow
**CRON Scheduler:**
* Location: `internal/workers/wrapped_scheduler/scheduler.go`
* Schedule: Every Sunday at midnight \(per user timezone\)
* Batch size: 100 users per run
**Generation Steps:**
1. Trigger: CRON job invokes `WeeklySummaryAggregator.Run()`
2. For each user:
    * Query portfolio performance \(week\-over\-week\)
    * Calculate top gainers/losers
    * Fetch contribution data \(deposits, round\-ups\)
    * Query investment streak
    * Get relevant news \(3\-5 articles\)
    * Calculate diversification metrics
3. Pass data to AI Orchestrator with "wrapped" prompt
4. AI generates:
    * Punchy headlines \("You're up 8\.3% this week 📈"\)
    * Personality insight \("You're a 'Steady Eddie' investor"\)
    * Top mover callout
    * Contribution summary
    * Relevant news
    * Optional action \("Consider Tech Growth basket"\)
5. Store as structured JSON in `ai_summaries.cards_json`
6. Send push notification
**Card Structure \(JSON\):**
```json
{
  "cards": [
    {
      "type": "performance_headline",
      "title": "This Week's Vibe",
      "content": "You're up 8.3% this week 📈",
      "data": {"weekly_return": 0.083}
    },
    {
      "type": "top_mover",
      "title": "Your MVP Stock",
      "content": "VTI carried the team with +12%",
      "data": {"symbol": "VTI", "return": 0.12}
    },
    {
      "type": "personality",
      "title": "Your Investing Style",
      "content": "Steady Eddie - consistent contributions, low volatility"
    },
    {
      "type": "contributions",
      "title": "Money Moves",
      "content": "$127 in deposits, $8 from round-ups",
      "data": {"deposits": 127, "roundups": 8}
    },
    {
      "type": "news",
      "title": "What's Happening",
      "content": "3 updates on your holdings",
      "articles": ["...", "...", "..."]
    },
    {
      "type": "suggestion",
      "title": "Consider This",
      "content": "Your tech exposure is high - diversify with Balanced Growth?",
      "action": {"type": "view_basket", "basket_id": "..."}
    }
  ]
}
```
### 6\. Chat Experience Implementation
**Session Management:**
* Each chat creates/resumes a session in `ai_chat_sessions`
* Messages stored in `ai_chat_messages`
* Context window: last 10 messages
* Session expires after 24 hours of inactivity
**Request Flow:**
```warp-runnable-command
POST /api/v1/ai/chat
{
  "message": "How's my portfolio doing?",
  "session_id": "optional-uuid"
}
→ ChatService.HandleMessage()
  → Load session context (last 10 messages)
  → Build AI request with tools
  → AI calls tools as needed (get_portfolio_stats, etc.)
  → Generate response
  → Store message in DB
  → Return response
Response:
{
  "message": "Your portfolio is up 3.2% this month...",
  "session_id": "uuid",
  "tool_calls": [{"name": "get_portfolio_stats", "result": {...}}]
}
```
**Safety Layer:**
* Filter out financial advice keywords
* If detected, rewrite to informational tone
* Log flagged interactions for review
## Implementation Phases
### Phase 1: Data Foundation \(Week 1\)
**Goal:** Set up data collection and storage
* Create database migrations for new tables
* Implement `UserContributionsRepository`
* Implement `InvestmentStreakRepository`
* Add contribution tracking to funding flows
* Add streak calculation worker
**Deliverables:**
* 6 new database tables created
* Repositories implemented
* Contribution tracking active
* Streak calculation working
### Phase 2: News Service \(Week 1\-2\)
**Goal:** Personalized news feed
* Integrate Alpaca News API or Finnhub
* Create `NewsService` with filtering logic
* Implement `UserNewsRepository`
* Create news aggregation CRON job \(hourly\)
* Build news API endpoints
**Deliverables:**
* News fetching and filtering working
* `GET /api/v1/news/feed` endpoint
* `GET /api/v1/news/weekly` endpoint
* Hourly news updates running
### Phase 3: Portfolio APIs \(Week 2\)
**Goal:** Expose portfolio data for AI
* Implement `GET /api/v1/portfolio/weekly-stats`
* Implement `GET /api/v1/portfolio/allocations`
* Implement `GET /api/v1/portfolio/top-movers`
* Implement `GET /api/v1/activity/contributions`
* Implement `GET /api/v1/activity/streak`
**Deliverables:**
* 5 new API endpoints
* Handler tests
* Integration tests
### Phase 4: AI Provider Implementation \(Week 2\)
**Goal:** Build OpenAI/Gemini integration
* Create `AIProvider` interface
* Implement `OpenAIProvider` with OpenAI API
* Implement `GeminiProvider` with Gemini API
* Add provider failover logic
* Implement rate limit handling
* Add token usage tracking
**Deliverables:**
* OpenAI integration working
* Gemini integration working
* Provider failover tested
* Rate limit handling verified
### Phase 5: Enhanced AI Service \(Week 2\-3\)
**Goal:** Upgrade AI capabilities
* Create `AIOrchestrator` service using new providers
* Implement tool calling infrastructure
* Design prompt templates for Wrapped
* Implement card generation logic
* Add response formatting
* Refactor existing `AICfoService` to use new AI providers
**Deliverables:**
* `AIOrchestrator` service operational
* Tool calling working with OpenAI/Gemini
* Card JSON generation working
* Prompt templates tested
* Old 0G code removed
### Phase 6: Weekly Wrapped \(Week 3\)
**Goal:** Generate Spotify\-style summaries
* Create `WeeklySummaryAggregator`
* Build CRON scheduler for Sunday runs
* Integrate with `AIOrchestrator`
* Store cards in `ai_summaries.cards_json`
* Implement `GET /api/v1/wrapped/latest`
* Implement `GET /api/v1/wrapped/history`
**Deliverables:**
* Weekly Wrapped generation working
* CRON job scheduled
* API endpoints for Wrapped
* Push notifications sent
### Phase 7: Basket Recommendations \(Week 3\-4\)
**Goal:** AI\-powered basket suggestions
* Create `BasketRecommender` service
* Implement recommendation logic
* Build `GET /api/v1/baskets/recommended`
* Create `POST /api/v1/baskets/preview-rebalance`
* Implement rebalance preview generation
**Deliverables:**
* Basket recommendations working
* Rebalance previews functional
* API endpoints tested
### Phase 8: Chat Experience \(Week 4\)
**Goal:** Interactive AI chat
* Create `ChatService`
* Implement session management
* Build `POST /api/v1/ai/chat`
* Build `GET /api/v1/ai/chat/history`
* Add safety filtering
* Integrate tool calling
**Deliverables:**
* Chat service operational
* Session management working
* Tool calls integrated
* Safety filters active
### Phase 9: Integration & Testing \(Week 4\-5\)
**Goal:** End\-to\-end validation
* Integration tests for all flows
* Load testing for AI endpoints
* Test Wrapped generation at scale
* Verify data accuracy
* Frontend integration testing
**Deliverables:**
* E2E tests passing
* Load tests successful
* Data accuracy validated
* Frontend integration complete
### Phase 10: Monitoring & Rollout \(Week 5\)
**Goal:** Production readiness
* Add OpenTelemetry tracing to all new services
* Create Prometheus metrics
* Build Grafana dashboards
* Set up CloudWatch alarms
* Gradual rollout \(10% → 50% → 100%\)
**Deliverables:**
* Full observability stack
* Dashboards and alerts configured
* Gradual rollout complete
## Configuration
**Environment Variables:**
```yaml
# AI Configuration
ai:
  primary_provider: "openai"  # or "gemini"
  fallback_provider: "gemini"
  openai:
    api_key: "${OPENAI_API_KEY}"
    model: "gpt-3.5-turbo"  # Free tier model
    max_tokens: 1000
    temperature: 0.7
    timeout: 30s
    rate_limit_rpm: 3  # Requests per minute (free tier limit)
  gemini:
    api_key: "${GEMINI_API_KEY}"
    model: "gemini-1.5-flash"  # Free tier model
    max_tokens: 1000
    temperature: 0.7
    timeout: 30s
    rate_limit_rpm: 15  # Requests per minute (free tier limit)
  prompts:
    wrapped_template: "prompts/wrapped.txt"
    chat_template: "prompts/chat.txt"
# News Configuration
news:
  provider: "alpaca"  # or "finnhub"
  api_key: "${NEWS_API_KEY}"
  fetch_interval: "1h"
  articles_per_user: 5
  relevance_threshold: 0.6
# Wrapped Configuration
wrapped:
  schedule: "0 0 * * 0"  # Sunday midnight
  batch_size: 100
  enable_notifications: true
  cards_enabled:
    - performance_headline
    - top_mover
    - personality
    - contributions
    - news
    - suggestion
# Chat Configuration
chat:
  max_context_messages: 10
  session_timeout: "24h"
  enable_safety_filter: true
  rate_limit_per_hour: 50
```
## Success Metrics
**User Engagement:**
* Weekly Wrapped open rate > 60%
* Chat interactions per user per week > 2
* Average session duration > 2 minutes
**AI Performance:**
* Tool call success rate > 95%
* Response time p95 < 3 seconds
* Safety filter accuracy > 99%
**Business Impact:**
* Basket adoption from recommendations > 10%
* Rebalance execution rate > 5%
* User retention improvement > 15%
## Risks & Mitigations
**Risk 1: AI Hallucination**
* Impact: Users receive inaccurate data
* Mitigation: Always use tool calls for data, never let AI invent numbers
**Risk 2: News API Rate Limits**
* Impact: Missing news for some users
* Mitigation: Multi\-provider fallback, aggressive caching
**Risk 3: Wrapped Generation Failures**
* Impact: Users miss weekly summary
* Mitigation: Retry logic, fallback to basic template if AI fails
**Risk 4: Chat Safety Issues**
* Impact: AI gives financial advice
* Mitigation: Safety filter layer, log all flagged interactions
**Risk 5: Performance at Scale**
* Impact: Slow response times under load
* Mitigation: Async generation for Wrapped, rate limiting, caching
**Risk 6: Free Tier Rate Limits**
* Impact: API rate limits hit during high usage
* Mitigation: Provider failover, request queuing, aggressive caching of responses
**Risk 7: Provider Outages**
* Impact: AI features unavailable
* Mitigation: Dual provider setup \(OpenAI \+ Gemini\), graceful degradation, cached fallbacks


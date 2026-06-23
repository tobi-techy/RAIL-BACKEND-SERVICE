package ai

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	infraai "github.com/rail-service/rail_service/internal/infrastructure/ai"
)

// InvestmentProduct represents a single investment option.
type InvestmentProduct struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Currency    string `json:"currency"`
	MinAmount   int    `json:"min_amount"`
	Risk        string `json:"risk"`
	ReturnRange string `json:"return_range"`
	LockPeriod  string `json:"lock_period"`
	Available   bool   `json:"available"`
}

var (
	investmentProducts     []InvestmentProduct
	investmentProductsOnce sync.Once
)

func loadInvestmentProducts() []InvestmentProduct {
	investmentProductsOnce.Do(func() {
		// Try loading from configs/ at runtime (relative to working dir)
		data, err := os.ReadFile("configs/investment_products.json")
		if err != nil {
			// Fallback: use embedded default
			_ = json.Unmarshal([]byte(defaultInvestmentProductsJSON), &investmentProducts)
			return
		}
		_ = json.Unmarshal(data, &investmentProducts)
	})
	return investmentProducts
}

const ToolGetInvestmentProducts = "get_investment_products"

// InvestmentProductTool returns the tool definition for investment product lookup.
func InvestmentProductTool() infraai.Tool {
	return infraai.Tool{
		Name:        ToolGetInvestmentProducts,
		Description: "Get available investment products filtered by currency, minimum amount, and risk level. Use when user asks about investing, where to put money, or product options.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"currency": map[string]interface{}{"type": "string", "description": "Filter by currency: USD, NGN, GBP. Optional."},
				"amount":   map[string]interface{}{"type": "number", "description": "Amount user wants to invest. Filters out products with higher minimums. Optional."},
				"risk":     map[string]interface{}{"type": "string", "description": "Filter by risk level: low, medium, high. Optional."},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
	}
}

// executeInvestmentProducts filters products by currency, amount, and risk.
func (o *Orchestrator) executeInvestmentProducts(args map[string]interface{}) (map[string]interface{}, error) {
	currency, _ := args["currency"].(string)
	risk, _ := args["risk"].(string)
	amount, _ := args["amount"].(float64)

	products := loadInvestmentProducts()
	var results []InvestmentProduct
	for _, p := range products {
		if !p.Available {
			continue
		}
		if currency != "" && !strings.EqualFold(p.Currency, currency) {
			continue
		}
		if risk != "" && !strings.EqualFold(p.Risk, risk) {
			continue
		}
		if amount > 0 && int(amount) < p.MinAmount {
			continue
		}
		results = append(results, p)
	}

	return map[string]interface{}{
		"products": results,
		"count":    len(results),
	}, nil
}

const defaultInvestmentProductsJSON = `[
  {"id":"rail_stash","name":"Rail Stash","description":"USD savings earning ~3-4% APY from US Treasuries. 90-day lock.","currency":"USD","min_amount":1,"risk":"low","return_range":"3-4% APY","lock_period":"90 days","available":true},
  {"id":"us_treasury_bills","name":"US Treasury Bills","description":"Short-term US government debt. Near-zero risk, 4-5% yield.","currency":"USD","min_amount":100,"risk":"low","return_range":"4-5% APY","lock_period":"4-52 weeks","available":true},
  {"id":"sp500_index","name":"S&P 500 Index Fund","description":"Broad US stock market exposure. Long-term growth, volatile short-term.","currency":"USD","min_amount":50,"risk":"medium","return_range":"8-12% historical avg","lock_period":"none","available":true},
  {"id":"ngn_money_market","name":"Naira Money Market Fund","description":"Short-term naira-denominated fund. Low risk, beats inflation slightly.","currency":"NGN","min_amount":5000,"risk":"low","return_range":"12-15% APY","lock_period":"none","available":true},
  {"id":"ngn_fixed_deposit","name":"Naira Fixed Deposit","description":"Fixed-term naira savings with guaranteed rate. Bank-backed.","currency":"NGN","min_amount":50000,"risk":"low","return_range":"14-18% APY","lock_period":"30-365 days","available":true},
  {"id":"global_tech_etf","name":"Global Tech ETF","description":"Concentrated exposure to top tech companies worldwide.","currency":"USD","min_amount":100,"risk":"high","return_range":"10-25% historical","lock_period":"none","available":true},
  {"id":"emerging_markets_etf","name":"Emerging Markets ETF","description":"Diversified exposure to developing economies including Africa.","currency":"USD","min_amount":50,"risk":"medium","return_range":"6-12% historical","lock_period":"none","available":true},
  {"id":"gbp_savings","name":"GBP High-Yield Savings","description":"Pound-denominated savings account with competitive rate.","currency":"GBP","min_amount":50,"risk":"low","return_range":"4-5% APY","lock_period":"none","available":true},
  {"id":"bitcoin_exposure","name":"Bitcoin Exposure (via ETF)","description":"Regulated Bitcoin ETF for crypto exposure without wallet management.","currency":"USD","min_amount":25,"risk":"high","return_range":"highly variable","lock_period":"none","available":false},
  {"id":"real_estate_reit","name":"Real Estate REIT","description":"Diversified real estate investment trust. Income + growth.","currency":"USD","min_amount":100,"risk":"medium","return_range":"6-10% APY","lock_period":"none","available":true},
  {"id":"ngn_eurobond","name":"Nigerian Eurobond Fund","description":"Dollar-denominated Nigerian government bonds. Higher yield, sovereign risk.","currency":"USD","min_amount":500,"risk":"medium","return_range":"8-11% APY","lock_period":"varies","available":true},
  {"id":"dividend_aristocrats","name":"Dividend Aristocrats ETF","description":"Companies with 25+ years of consecutive dividend increases.","currency":"USD","min_amount":100,"risk":"medium","return_range":"3-5% dividend + growth","lock_period":"none","available":true}
]`

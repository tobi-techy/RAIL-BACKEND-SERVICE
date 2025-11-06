package prompts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/stack-service/stack_service/internal/domain/entities"
)

// PromptTemplate represents a template for AI prompts
type PromptTemplate struct {
	Name        string
	System      string
	UserTemplate string
	template    *template.Template
}

// WeeklySummaryContext contains data for weekly summary generation
type WeeklySummaryContext struct {
	UserID         uuid.UUID
	WeekStart      time.Time
	WeekEnd        time.Time
	Portfolio      *entities.PortfolioMetrics
	PreviousWeek   *entities.PortfolioMetrics
	Preferences    *entities.UserPreferences
	MarketContext  *MarketContext
}

// OnDemandAnalysisContext contains data for on-demand analysis
type OnDemandAnalysisContext struct {
	UserID        uuid.UUID
	AnalysisType  string
	Portfolio     *entities.PortfolioMetrics
	Preferences   *entities.UserPreferences
	Parameters    map[string]interface{}
	MarketContext *MarketContext
}

// MarketContext provides general market information
type MarketContext struct {
	MarketTrend    string  // bullish, bearish, sideways
	VIXLevel       float64 // Market volatility index
	InterestRates  float64 // Current risk-free rate
	EconomicEvents []string // Recent significant economic events
}

// TemplateManager manages prompt templates
type TemplateManager struct {
	templates map[string]*PromptTemplate
}

// NewTemplateManager creates a new template manager
func NewTemplateManager() *TemplateManager {
	manager := &TemplateManager{
		templates: make(map[string]*PromptTemplate),
	}
	
	// Initialize default templates
	manager.initializeDefaultTemplates()
	
	return manager
}

// initializeDefaultTemplates sets up the default prompt templates
func (tm *TemplateManager) initializeDefaultTemplates() {
	// Weekly Summary Template
	weeklyTemplate := &PromptTemplate{
		Name: "weekly_summary",
		System: `You are an AI-powered Chief Financial Officer (CFO) assistant for a portfolio management platform. Your role is to provide insightful, professional weekly portfolio analysis and summaries.

IMPORTANT GUIDELINES:
- You are providing DESCRIPTIVE ANALYTICS only, not personalized investment advice
- Include clear disclaimers that this is not financial advice
- Focus on portfolio performance metrics, risk analysis, and asset allocation insights
- Use professional, clear language suitable for retail investors
- Provide actionable insights without recommending specific trades
- Never include personally identifiable information (PII) or sensitive KYC data
- Keep summaries concise but comprehensive (aim for 800-1200 words in markdown format)

Your analysis should include:
1. Performance overview and key metrics
2. Risk assessment and portfolio health
3. Asset allocation analysis
4. Notable changes from previous week (if available)
5. Market context and its impact on the portfolio
6. Areas of attention or potential improvements (general guidance only)`,

		UserTemplate: `Please analyze this portfolio for the week of {{.WeekStart.Format "January 2, 2006"}} to {{.WeekEnd.Format "January 2, 2006"}}.

**Portfolio Overview:**
- Total Value: ${{printf "%.2f" .Portfolio.TotalValue}}
- Total Return: {{printf "%.2f" .Portfolio.TotalReturnPct}}%
- Week Change: {{printf "%.2f" .Portfolio.WeekChangePct}}%
- Day Change: {{printf "%.2f" .Portfolio.DayChangePct}}%
- Month Change: {{printf "%.2f" .Portfolio.MonthChangePct}}%

**Risk Metrics:**
{{if .Portfolio.RiskMetrics}}
- Volatility: {{printf "%.2f" .Portfolio.RiskMetrics.Volatility}}%
- Sharpe Ratio: {{printf "%.2f" .Portfolio.RiskMetrics.SharpeRatio}}
- Max Drawdown: {{printf "%.2f" .Portfolio.RiskMetrics.MaxDrawdown}}%
- Diversification Score: {{printf "%.2f" .Portfolio.RiskMetrics.Diversification}}
{{end}}

**Current Positions:**
{{range .Portfolio.Positions}}
- {{.BasketName}}: {{printf "%.1f" .Weight}}% ({{printf "%.2f" .UnrealizedPLPct}}% unrealized P&L)
{{end}}

**User Preferences:**
- Risk Tolerance: {{.Preferences.RiskTolerance}}
- Preferred Style: {{.Preferences.PreferredStyle}}
- Focus Areas: {{join .Preferences.FocusAreas ", "}}

{{if .PreviousWeek}}
**Previous Week Comparison:**
- Previous Total Value: ${{printf "%.2f" .PreviousWeek.TotalValue}}
- Value Change: ${{printf "%.2f" (subtract .Portfolio.TotalValue .PreviousWeek.TotalValue)}}
{{end}}

{{if .MarketContext}}
**Market Context:**
- Market Trend: {{.MarketContext.MarketTrend}}
- VIX Level: {{printf "%.2f" .MarketContext.VIXLevel}}
- Interest Rates: {{printf "%.2f" .MarketContext.InterestRates}}%
{{if .MarketContext.EconomicEvents}}
- Recent Events: {{join .MarketContext.EconomicEvents ", "}}
{{end}}
{{end}}

Please provide a comprehensive weekly portfolio summary in markdown format. Include:
1. Executive summary of performance
2. Key performance highlights and concerns  
3. Risk analysis and portfolio health assessment
4. Asset allocation insights
5. Week-over-week changes and their implications
6. Market impact analysis
7. General areas for consideration (without specific trade recommendations)

Remember to include appropriate disclaimers and keep the analysis descriptive and educational.`,
	}

	// On-Demand Analysis Templates
	riskAnalysisTemplate := &PromptTemplate{
		Name: "risk_analysis",
		System: `You are an AI-powered portfolio risk analyst. Provide professional risk analysis and insights without giving personalized investment advice.

Focus on:
- Risk metrics interpretation
- Portfolio volatility analysis  
- Diversification assessment
- Risk-return profile evaluation
- General risk management insights

Always include disclaimers and avoid specific trade recommendations.`,

		UserTemplate: `Analyze the risk profile of this portfolio:

**Portfolio Metrics:**
- Total Value: ${{printf "%.2f" .Portfolio.TotalValue}}
- Total Return: {{printf "%.2f" .Portfolio.TotalReturnPct}}%

**Risk Metrics:**
{{if .Portfolio.RiskMetrics}}
- Volatility: {{printf "%.2f" .Portfolio.RiskMetrics.Volatility}}%
- Beta: {{printf "%.2f" .Portfolio.RiskMetrics.Beta}}
- Sharpe Ratio: {{printf "%.2f" .Portfolio.RiskMetrics.SharpeRatio}}
- Max Drawdown: {{printf "%.2f" .Portfolio.RiskMetrics.MaxDrawdown}}%
- VaR (95%): {{printf "%.2f" .Portfolio.RiskMetrics.VaR}}%
- Diversification Score: {{printf "%.2f" .Portfolio.RiskMetrics.Diversification}}
{{end}}

**Positions:**
{{range .Portfolio.Positions}}
- {{.BasketName}}: {{printf "%.1f" .Weight}}% allocation
{{end}}

**User Risk Tolerance:** {{.Preferences.RiskTolerance}}

Provide a detailed risk analysis in markdown format covering:
1. Overall risk assessment
2. Volatility analysis
3. Diversification evaluation
4. Risk-adjusted performance
5. Potential risk factors
6. General risk management considerations`,
	}

	performanceAnalysisTemplate := &PromptTemplate{
		Name: "performance_analysis",
		System: `You are an AI-powered portfolio performance analyst. Provide professional performance analysis and insights without giving personalized investment advice.

Focus on:
- Performance metrics interpretation
- Return analysis across time periods
- Performance attribution
- Benchmark comparisons (when available)
- Performance trends and patterns

Always include disclaimers and avoid specific trade recommendations.`,

		UserTemplate: `Analyze the performance of this portfolio:

**Performance Metrics:**
- Total Value: ${{printf "%.2f" .Portfolio.TotalValue}}
- Total Return: {{printf "%.2f" .Portfolio.TotalReturn}} ({{printf "%.2f" .Portfolio.TotalReturnPct}}%)
- Day Change: {{printf "%.2f" .Portfolio.DayChange}} ({{printf "%.2f" .Portfolio.DayChangePct}}%)
- Week Change: {{printf "%.2f" .Portfolio.WeekChange}} ({{printf "%.2f" .Portfolio.WeekChangePct}}%)
- Month Change: {{printf "%.2f" .Portfolio.MonthChange}} ({{printf "%.2f" .Portfolio.MonthChangePct}}%)

**Risk-Adjusted Performance:**
{{if .Portfolio.RiskMetrics}}
- Sharpe Ratio: {{printf "%.2f" .Portfolio.RiskMetrics.SharpeRatio}}
- Max Drawdown: {{printf "%.2f" .Portfolio.RiskMetrics.MaxDrawdown}}%
{{end}}

**Position Performance:**
{{range .Portfolio.Positions}}
- {{.BasketName}}: {{printf "%.2f" .UnrealizedPLPct}}% unrealized P&L ({{printf "%.1f" .Weight}}% weight)
{{end}}

Provide a detailed performance analysis in markdown format covering:
1. Overall performance assessment
2. Short and medium-term performance trends
3. Risk-adjusted performance evaluation
4. Position-level performance insights
5. Performance consistency analysis
6. Areas of strength and improvement`,
	}

	diversificationAnalysisTemplate := &PromptTemplate{
		Name: "diversification_analysis",
		System: `You are an AI-powered portfolio diversification analyst. Provide professional diversification analysis and insights without giving personalized investment advice.

Focus on:
- Diversification metrics interpretation
- Asset allocation analysis
- Concentration risk assessment
- Correlation analysis insights
- Diversification effectiveness

Always include disclaimers and avoid specific trade recommendations.`,

		UserTemplate: `Analyze the diversification of this portfolio:

**Portfolio Allocation:**
{{range .Portfolio.Positions}}
- {{.BasketName}}: {{printf "%.1f" .Weight}}% (${{printf "%.2f" .CurrentValue}})
{{end}}

**Diversification Metrics:**
{{if .Portfolio.RiskMetrics}}
- Diversification Score: {{printf "%.2f" .Portfolio.RiskMetrics.Diversification}}
- Portfolio Volatility: {{printf "%.2f" .Portfolio.RiskMetrics.Volatility}}%
{{end}}

**Allocation by Basket:**
{{range $basket, $allocation := .Portfolio.AllocationByBasket}}
- {{$basket}}: {{printf "%.1f" $allocation}}%
{{end}}

**User Risk Tolerance:** {{.Preferences.RiskTolerance}}

Provide a detailed diversification analysis in markdown format covering:
1. Current diversification assessment
2. Concentration risk analysis
3. Asset allocation evaluation
4. Diversification effectiveness
5. Potential diversification improvements
6. Risk reduction opportunities`,
	}

	allocationAnalysisTemplate := &PromptTemplate{
		Name: "allocation_analysis",
		System: `You are an AI-powered portfolio allocation analyst. Provide professional asset allocation analysis and insights without giving personalized investment advice.

Focus on:
- Current allocation breakdown
- Allocation drift analysis
- Target allocation discussions
- Rebalancing considerations
- Allocation strategy insights

Always include disclaimers and avoid specific trade recommendations.`,

		UserTemplate: `Analyze the asset allocation of this portfolio:

**Current Allocation:**
{{range .Portfolio.Positions}}
- {{.BasketName}}: {{printf "%.1f" .Weight}}% (Target weight considerations)
{{end}}

**Allocation by Category:**
{{range $category, $allocation := .Portfolio.AllocationByBasket}}
- {{$category}}: {{printf "%.1f" $allocation}}%
{{end}}

**Portfolio Value:** ${{printf "%.2f" .Portfolio.TotalValue}}
**User Risk Tolerance:** {{.Preferences.RiskTolerance}}

{{if .Parameters.target_allocations}}
**Target Allocations (if provided):**
{{range $asset, $target := .Parameters.target_allocations}}
- {{$asset}}: {{$target}}%
{{end}}
{{end}}

Provide a detailed allocation analysis in markdown format covering:
1. Current allocation breakdown
2. Allocation concentration analysis
3. Risk-appropriate allocation assessment
4. Potential allocation drift considerations
5. Rebalancing insights
6. Allocation optimization opportunities`,
	}

	rebalancingAnalysisTemplate := &PromptTemplate{
		Name: "rebalancing_analysis", 
		System: `You are an AI-powered portfolio rebalancing analyst. Provide professional rebalancing analysis and insights without giving personalized investment advice.

Focus on:
- Rebalancing need assessment
- Allocation drift analysis
- Rebalancing impact evaluation
- Cost-benefit considerations
- Timing considerations

Always include disclaimers and avoid specific trade recommendations.`,

		UserTemplate: `Analyze rebalancing considerations for this portfolio:

**Current Allocation:**
{{range .Portfolio.Positions}}
- {{.BasketName}}: {{printf "%.1f" .Weight}}% ({{printf "%.2f" .UnrealizedPLPct}}% P&L)
{{end}}

**Portfolio Metrics:**
- Total Value: ${{printf "%.2f" .Portfolio.TotalValue}}
- Recent Performance: {{printf "%.2f" .Portfolio.WeekChangePct}}% (week), {{printf "%.2f" .Portfolio.MonthChangePct}}% (month)

**Risk Profile:**
{{if .Portfolio.RiskMetrics}}
- Diversification Score: {{printf "%.2f" .Portfolio.RiskMetrics.Diversification}}
- Volatility: {{printf "%.2f" .Portfolio.RiskMetrics.Volatility}}%
{{end}}

**User Preferences:** {{.Preferences.RiskTolerance}} risk tolerance

Provide a detailed rebalancing analysis in markdown format covering:
1. Rebalancing need assessment
2. Current allocation vs. optimal considerations
3. Risk impact of current allocation
4. Rebalancing benefits and costs
5. Timing considerations
6. Implementation insights`,
	}

	// Compile templates
	templates := []*PromptTemplate{
		weeklyTemplate,
		riskAnalysisTemplate,
		performanceAnalysisTemplate,
		diversificationAnalysisTemplate,
		allocationAnalysisTemplate,
		rebalancingAnalysisTemplate,
	}

	for _, tmpl := range templates {
		if err := tm.compileTemplate(tmpl); err != nil {
			// Log error but continue with other templates
			continue
		}
		tm.templates[tmpl.Name] = tmpl
	}
}

// compileTemplate compiles the user template for a prompt template
func (tm *TemplateManager) compileTemplate(tmpl *PromptTemplate) error {
	// Create custom template functions
	funcMap := template.FuncMap{
		"join": strings.Join,
		"subtract": func(a, b float64) float64 {
			return a - b
		},
		"formatCurrency": func(amount float64) string {
			return fmt.Sprintf("$%.2f", amount)
		},
		"formatPercent": func(percent float64) string {
			return fmt.Sprintf("%.2f%%", percent)
		},
		"abs": func(x float64) float64 {
			if x < 0 {
				return -x
			}
			return x
		},
	}

	compiled, err := template.New(tmpl.Name).Funcs(funcMap).Parse(tmpl.UserTemplate)
	if err != nil {
		return fmt.Errorf("failed to compile template %s: %w", tmpl.Name, err)
	}

	tmpl.template = compiled
	return nil
}

// GenerateWeeklySummaryPrompt generates a prompt for weekly portfolio summary
func (tm *TemplateManager) GenerateWeeklySummaryPrompt(ctx *WeeklySummaryContext) (string, string, error) {
	tmpl, exists := tm.templates["weekly_summary"]
	if !exists {
		return "", "", fmt.Errorf("weekly summary template not found")
	}

	userPrompt, err := tm.executeTemplate(tmpl, ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to execute weekly summary template: %w", err)
	}

	return tmpl.System, userPrompt, nil
}

// GenerateOnDemandAnalysisPrompt generates a prompt for on-demand analysis
func (tm *TemplateManager) GenerateOnDemandAnalysisPrompt(ctx *OnDemandAnalysisContext) (string, string, error) {
	templateName := getAnalysisTemplateName(ctx.AnalysisType)
	tmpl, exists := tm.templates[templateName]
	if !exists {
		return "", "", fmt.Errorf("template not found for analysis type: %s", ctx.AnalysisType)
	}

	userPrompt, err := tm.executeTemplate(tmpl, ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to execute analysis template: %w", err)
	}

	return tmpl.System, userPrompt, nil
}

// executeTemplate executes a template with the given context
func (tm *TemplateManager) executeTemplate(tmpl *PromptTemplate, ctx interface{}) (string, error) {
	if tmpl.template == nil {
		return "", fmt.Errorf("template not compiled")
	}

	var buf bytes.Buffer
	if err := tmpl.template.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// getAnalysisTemplateName maps analysis types to template names
func getAnalysisTemplateName(analysisType string) string {
	switch analysisType {
	case entities.AnalysisTypeRisk:
		return "risk_analysis"
	case entities.AnalysisTypePerformance:
		return "performance_analysis"
	case entities.AnalysisTypeDiversification:
		return "diversification_analysis"
	case entities.AnalysisTypeAllocation:
		return "allocation_analysis"
	case entities.AnalysisTypeRebalancing:
		return "rebalancing_analysis"
	default:
		return "risk_analysis" // Default fallback
	}
}

// GetAvailableTemplates returns a list of available template names
func (tm *TemplateManager) GetAvailableTemplates() []string {
	var names []string
	for name := range tm.templates {
		names = append(names, name)
	}
	return names
}

// AddCustomTemplate adds a custom template to the manager
func (tm *TemplateManager) AddCustomTemplate(tmpl *PromptTemplate) error {
	if err := tm.compileTemplate(tmpl); err != nil {
		return fmt.Errorf("failed to compile custom template: %w", err)
	}
	
	tm.templates[tmpl.Name] = tmpl
	return nil
}

// ValidateTemplateContext validates that the context has required fields
func ValidateWeeklySummaryContext(ctx *WeeklySummaryContext) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if ctx.Portfolio == nil {
		return fmt.Errorf("portfolio data is required")
	}
	if ctx.Preferences == nil {
		return fmt.Errorf("user preferences are required")
	}
	if ctx.WeekStart.IsZero() || ctx.WeekEnd.IsZero() {
		return fmt.Errorf("week start and end dates are required")
	}
	return nil
}

// ValidateAnalysisContext validates on-demand analysis context
func ValidateAnalysisContext(ctx *OnDemandAnalysisContext) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	if ctx.Portfolio == nil {
		return fmt.Errorf("portfolio data is required")
	}
	if ctx.Preferences == nil {
		return fmt.Errorf("user preferences are required")
	}
	if ctx.AnalysisType == "" {
		return fmt.Errorf("analysis type is required")
	}
	return nil
}

// CreateDefaultMarketContext creates a default market context
func CreateDefaultMarketContext() *MarketContext {
	return &MarketContext{
		MarketTrend:    "sideways",
		VIXLevel:       20.0, // Moderate volatility
		InterestRates:  5.0,  // Current approximate rate
		EconomicEvents: []string{"Monthly employment report", "Federal Reserve meeting"},
	}
}

// FormatJSONContext formats context data as JSON for debugging
func FormatJSONContext(ctx interface{}) string {
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error formatting context: %v", err)
	}
	return string(data)
}
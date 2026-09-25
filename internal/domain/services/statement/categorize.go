package statement

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Canonical statement buckets. The enrichment sidecar's spend_bucket and
// Miriam's document categorizer use the same names.
const (
	BucketFood          = "food"
	BucketGroceries     = "groceries"
	BucketTransport     = "transport"
	BucketUtilities     = "utilities"
	BucketEntertainment = "entertainment"
	BucketShopping      = "shopping"
	BucketHealth        = "health"
	BucketEducation     = "education"
	BucketRent          = "rent"
	BucketTransferIn    = "transfer_in"
	BucketTransferOut   = "transfer_out"
	BucketSalary        = "salary"
	BucketAirtime       = "airtime"
	BucketBetting       = "betting"
	BucketSubscription  = "subscription"
	BucketSavings       = "savings"
	BucketLoan          = "loan"
	BucketFees          = "fees"
	BucketATM           = "atm"
	BucketOther         = "other"
)

// allowedBuckets is the closed set the statement parser may store.
var allowedBuckets = map[string]struct{}{
	BucketFood: {}, BucketGroceries: {}, BucketTransport: {}, BucketUtilities: {},
	BucketEntertainment: {}, BucketShopping: {}, BucketHealth: {}, BucketEducation: {},
	BucketRent: {}, BucketTransferIn: {}, BucketTransferOut: {}, BucketSalary: {},
	BucketAirtime: {}, BucketBetting: {}, BucketSubscription: {}, BucketSavings: {},
	BucketLoan: {}, BucketFees: {}, BucketATM: {}, BucketOther: {},
}

// categoryAliases collapses labels the model invents onto the closed set.
var categoryAliases = map[string]string{
	"grocery": "groceries", "groceries": "groceries", "supermarket": "groceries",
	"food": "food", "dining": "food", "restaurant": "food", "fast food": "food",
	"food & drink": "food", "food and drink": "food",
	"transport": "transport", "transportation": "transport", "fuel": "transport", "ride": "transport",
	"utilities": "utilities", "utility": "utilities", "bills": "utilities", "electricity": "utilities",
	"entertainment": "entertainment",
	"shopping":      "shopping", "retail": "shopping",
	"health": "health", "medical": "health", "pharmacy": "health",
	"education": "education", "school": "education",
	"rent": "rent", "housing": "rent",
	"transfer_in": "transfer_in", "transfer in": "transfer_in", "inward transfer": "transfer_in",
	"transfer_out": "transfer_out", "transfer out": "transfer_out", "outward transfer": "transfer_out",
	"salary": "salary", "payroll": "salary", "income": "salary",
	"airtime": "airtime", "data": "airtime", "phone": "airtime", "telecom": "airtime",
	"betting": "betting", "gambling": "betting", "bet": "betting",
	"subscription": "subscription", "subscriptions": "subscription",
	"savings": "savings",
	"loan":    "loan", "debt": "loan",
	"fees": "fees", "fee": "fees", "charges": "fees", "bank fee": "fees", "bank fees": "fees",
	"atm": "atm", "cash": "atm", "cash withdrawal": "atm",
	"other": "other", "uncategorized": "other", "unknown": "other", "miscellaneous": "other",
}

type narrationRule struct {
	re        *regexp.Regexp
	bucket    string
	overrides bool
}

// narrationRules mirror services/enrichment/src/spend_rules.py. First match wins.
var narrationRules = []narrationRule{
	{regexp.MustCompile(`(?i)\b(bet9ja|sportybet|betking|1xbet|betway|nairabet|msport|bangbet)\b`), BucketBetting, true},
	{regexp.MustCompile(`(?i)\b(salary|payroll|wages?)\b`), BucketSalary, true},
	{regexp.MustCompile(`(?i)\b(sms\s*alert|stamp\s*duty|account\s*maintenance|vat\s+on|commission|card\s+maintenance|transfer\s+charge)\b`), BucketFees, true},
	{regexp.MustCompile(`(?i)\b(atm|cash\s+withdrawal|atm\s*wdl|atm\s+cash)\b`), BucketATM, true},
	{regexp.MustCompile(`(?i)\b(airtime|data\s+bundle|data\s+recharge|mtn|glo|airtel|9\s*mobile)\b`), BucketAirtime, true},
	{regexp.MustCompile(`(?i)\b(ikedc|aedc|ekedc|ibedc|phed|kedco|eedc|jedc|phcn|nepa|lawma)\b`), BucketUtilities, true},
	{regexp.MustCompile(`(?i)\b(dstv|gotv|startimes|showmax|netflix|spotify|youtube\s*premium)\b`), BucketSubscription, true},
	{regexp.MustCompile(`(?i)\b(rent|landlord)\b`), BucketRent, true},
	{regexp.MustCompile(`(?i)\b(shoprite|spar|justrite|ebeano|game\s+store|prince\s+ebean|supermarket|grocer\w*)\b`), BucketGroceries, true},
	{regexp.MustCompile(`(?i)\b(chicken\s+republic|kfc|mr\s+biggs|dominos?|pizza\s+hut|coldstone|bukka|chowdeck|glovo|restaurant|eatery)\b`), BucketFood, false},
	{regexp.MustCompile(`(?i)\b(uber|bolt|indrive|in-?drive|taxify|fuel|filling\s+station|totalenergies|nnpc|oando)\b`), BucketTransport, false},
	{regexp.MustCompile(`(?i)\b(pharmacy|healthplus|medplus|hospital|clinic|laboratory|\blab\b)\b`), BucketHealth, true},
	{regexp.MustCompile(`(?i)\b(tuition|school\s+fee|university|waec|jamb|neco)\b`), BucketEducation, true},
	{regexp.MustCompile(`(?i)\b(piggyvest|cowrywise|stash|savings\s+deposit)\b`), BucketSavings, true},
	{regexp.MustCompile(`(?i)\b(loan\s+repay|loan\s+deduction|loan\s+repayment)\b`), BucketLoan, true},
	// NOTE: transfer_in must stay before transfer_out (first match wins).
	// "NIP CREDIT ..." contains "nip" — if transfer_out ran first every
	// incoming transfer would be labelled outgoing. Keep aligned with
	// services/enrichment/src/spend_rules.py.
	{regexp.MustCompile(`(?i)\b(nip\s+credit|transfer\s+from|received\s+from|inward\s+transfer)\b`), BucketTransferIn, true},
	{regexp.MustCompile(`(?i)\b(nip|trf\s+to|transfer\s+to|sent\s+to|funds?\s+transfer)\b`), BucketTransferOut, true},
}

// CategoryLabel is the phrase Miriam should say for a bucket.
func CategoryLabel(bucket string) string {
	switch bucket {
	case BucketFood:
		return "Eating out"
	case BucketGroceries:
		return "Groceries"
	case BucketTransport:
		return "Transport"
	case BucketUtilities:
		return "Utilities"
	case BucketEntertainment:
		return "Entertainment"
	case BucketShopping:
		return "Shopping"
	case BucketHealth:
		return "Health"
	case BucketEducation:
		return "Education"
	case BucketRent:
		return "Rent"
	case BucketTransferIn:
		return "Transfers in"
	case BucketTransferOut:
		return "Transfers out"
	case BucketSalary:
		return "Salary"
	case BucketAirtime:
		return "Airtime and data"
	case BucketBetting:
		return "Betting"
	case BucketSubscription:
		return "Subscriptions"
	case BucketSavings:
		return "Savings"
	case BucketLoan:
		return "Loan payments"
	case BucketFees:
		return "Bank fees"
	case BucketATM:
		return "Cash withdrawals"
	default:
		return "Other"
	}
}

// IsConsumptionSpend is true for money that left as a purchase, bill, or fee.
// Transfers, savings moves, and loan repayments are money movement: counting
// them as spending double-counts cash the user still has or already recorded
// as a bill.
func IsConsumptionSpend(bucket string) bool {
	switch bucket {
	case BucketTransferIn, BucketTransferOut, BucketSalary, BucketSavings, BucketLoan:
		return false
	default:
		return true
	}
}

// NormalizeStatementCategory maps an LLM label onto the closed bucket set.
// A high-confidence narration (betting, salary, a named utility) overrides a
// contradictory label. A weak or empty label is filled from the description.
func NormalizeStatementCategory(category, description, txnType string) string {
	cat := canonicalBucket(category)
	if isBareTransfer(category) {
		if strings.EqualFold(txnType, "credit") {
			cat = BucketTransferIn
		} else {
			cat = BucketTransferOut
		}
	}
	inferred, overrides := inferBucket(description)
	if inferred == "" {
		if cat == "" {
			return directionFallback(txnType)
		}
		return cat
	}
	if cat == "" || cat == BucketOther || !knownBucket(cat) || (overrides && cat != inferred) {
		return inferred
	}
	return cat
}

// PartitionSpend splits debit totals into consumption and money movement.
// Keys are normalized so "Groceries" and "groceries" land in one bucket.
func PartitionSpend(byCategory map[string]float64) (consumption, movement map[string]float64) {
	consumption = map[string]float64{}
	movement = map[string]float64{}
	for raw, amount := range byCategory {
		bucket := NormalizeStatementCategory(raw, "", "debit")
		if !IsConsumptionSpend(bucket) {
			movement[bucket] += amount
			continue
		}
		consumption[bucket] += amount
	}
	return consumption, movement
}

func canonicalBucket(category string) string {
	cat := strings.ToLower(strings.TrimSpace(category))
	cat = strings.ReplaceAll(cat, "-", " ")
	cat = strings.Join(strings.Fields(cat), " ")
	if cat == "" {
		return ""
	}
	if mapped, ok := categoryAliases[cat]; ok {
		return mapped
	}
	if knownBucket(cat) {
		return cat
	}
	return ""
}

func isBareTransfer(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "transfer", "p2p", "p2p transfer":
		return true
	default:
		return false
	}
}

func knownBucket(bucket string) bool {
	_, ok := allowedBuckets[bucket]
	return ok
}

// AllowedStatementBucket reports whether bucket is in the closed set.
func AllowedStatementBucket(bucket string) bool {
	return knownBucket(canonicalBucket(bucket))
}

// AdviceConfidenceFloor is the minimum category confidence included in
// spending advice. Below this, the line is kept but not treated as a
// known purchase.
const AdviceConfidenceFloor = 0.5

// LineUnderstanding is what Miriam is allowed to read about one statement line.
type LineUnderstanding struct {
	Bucket       string
	Counterparty string
	Essential    bool
	Confidence   float64
}

// UnderstandLine categorizes from the narration. The model's label is only a
// hint, and only when the narration has no stronger signal. Confidence is
// high for a narration rule, medium for a model hint, and low for other.
func UnderstandLine(modelCategory, description, txnType string) LineUnderstanding {
	bucket := NormalizeStatementCategory(modelCategory, description, txnType)
	_, narrationHit := inferBucket(description)
	confidence := 0.25
	switch {
	case narrationHit:
		confidence = 0.9
	case bucket != BucketOther && canonicalBucket(modelCategory) == bucket:
		confidence = 0.55
	case bucket != BucketOther:
		confidence = 0.7
	}
	return LineUnderstanding{
		Bucket:       bucket,
		Counterparty: ExtractCounterparty(description),
		Essential:    IsEssentialBucket(bucket),
		Confidence:   confidence,
	}
}

// IsEssentialBucket marks necessities. Transfers and fees are not essential
// purchases even when they are real.
func IsEssentialBucket(bucket string) bool {
	switch bucket {
	case BucketGroceries, BucketUtilities, BucketHealth, BucketEducation, BucketRent, BucketAirtime, BucketSalary:
		return true
	default:
		return false
	}
}

var (
	nipCounterparty = regexp.MustCompile(`(?i)\bnip\s+[^/\s]+/([^/]+)`)
	posPurchaseLine = regexp.MustCompile(`(?i)pos(?:\s*/\s*web)?\s+purchase\s*[-:]?\s*(.+)`)
	leadingRail     = regexp.MustCompile(`(?i)^(pos|web|trf|transfer|payment|debit|credit)\b[\s:/-]*`)
	trailingRef     = regexp.MustCompile(`(?i)[\s/#_-]*\d{3,}\s*$`)
	recurrenceNoise = regexp.MustCompile(`[^a-z0-9]+`)
)

// broadContainsPatterns are tokens that match too many narrations to be a
// contains-rule. An exact match on the full description is still allowed.
var broadContainsPatterns = map[string]struct{}{
	"pos": {}, "nip": {}, "web": {}, "trf": {}, "from": {}, "pay": {},
	"fee": {}, "vat": {}, "atm": {}, "transfer": {}, "payment": {},
	"debit": {}, "credit": {}, "ngn": {}, "usd": {}, "the": {}, "and": {},
	"for": {}, "to": {},
}

// ExtractCounterparty pulls the person or merchant out of a narration.
// A NIP line yields the name segment, not the bank code.
func ExtractCounterparty(description string) string {
	text := strings.TrimSpace(description)
	if text == "" {
		return ""
	}
	if m := nipCounterparty.FindStringSubmatch(text); len(m) == 2 {
		name := strings.TrimSpace(m[1])
		if name != "" && !onlyDigits(name) {
			return titleShort(name)
		}
	}
	if m := posPurchaseLine.FindStringSubmatch(text); len(m) == 2 {
		return titleShort(m[1])
	}
	cleaned := leadingRail.ReplaceAllString(text, "")
	cleaned = trailingRef.ReplaceAllString(cleaned, "")
	return titleShort(cleaned)
}

// ValidateCategoryRule rejects corrections that would rewrite unrelated lines.
// contains-matches must be at least 3 characters and must not be a generic
// rail token such as "pos" or "transfer".
func ValidateCategoryRule(matchType, pattern, bucket string) (string, string, string, error) {
	pattern = strings.TrimSpace(pattern)
	if utf8.RuneCountInString(pattern) < 3 {
		return "", "", "", fmt.Errorf("pattern must be at least 3 characters")
	}
	if utf8.RuneCountInString(pattern) > 80 {
		return "", "", "", fmt.Errorf("pattern must be at most 80 characters")
	}
	switch strings.ToLower(strings.TrimSpace(matchType)) {
	case "", entities.StatementRuleContains:
		matchType = entities.StatementRuleContains
		if _, broad := broadContainsPatterns[strings.ToLower(pattern)]; broad {
			return "", "", "", fmt.Errorf("pattern %q is too broad for a contains match", pattern)
		}
	case entities.StatementRuleExact:
		matchType = entities.StatementRuleExact
	default:
		return "", "", "", fmt.Errorf("match_type must be contains or exact")
	}
	if !AllowedStatementBucket(bucket) {
		return "", "", "", fmt.Errorf("bucket must be one of the statement categories")
	}
	canonical := NormalizeStatementCategory(bucket, "", "debit")
	return matchType, pattern, canonical, nil
}

// SummarizeForChat is the short reading Miriam can say after a statement
// scan. Categories come from the narration rules, not from whatever label
// the extractor left blank.
func SummarizeForChat(result *ParseResult) string {
	if result == nil || len(result.Transactions) == 0 {
		return ""
	}
	var income, spending float64
	categories := map[string]float64{}
	for _, txn := range result.Transactions {
		understood := UnderstandLine(txn.Category, txn.Description, txn.Type)
		if strings.EqualFold(txn.Type, "credit") {
			if understood.Bucket != BucketTransferIn && understood.Bucket != BucketSavings && understood.Bucket != BucketLoan {
				income += txn.Amount
			}
			continue
		}
		if !IsConsumptionSpend(understood.Bucket) {
			continue
		}
		spending += txn.Amount
		categories[understood.Bucket] += txn.Amount
	}
	type categoryTotal struct {
		name  string
		total float64
	}
	sorted := make([]categoryTotal, 0, len(categories))
	for name, total := range categories {
		sorted = append(sorted, categoryTotal{name: CategoryLabel(name), total: total})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].total > sorted[j].total })
	top := make([]string, 0, 3)
	for i := 0; i < len(sorted) && i < 3; i++ {
		top = append(top, fmt.Sprintf("%s %.0f", sorted[i].name, sorted[i].total))
	}
	currency := result.Currency
	if currency == "" {
		currency = "NGN"
	}
	summary := fmt.Sprintf("I found %d transactions. Income was about %s %.0f and spending about %s %.0f", len(result.Transactions), currency, income, currency, spending)
	if len(top) > 0 {
		summary += ". Biggest spending areas: " + strings.Join(top, ", ")
	}
	return summary + "."
}

func recurrenceKey(counterparty, description string) string {
	s := strings.TrimSpace(counterparty)
	if s == "" {
		s = description
	}
	s = recurrenceNoise.ReplaceAllString(strings.ToLower(s), " ")
	fields := strings.Fields(s)
	if len(fields) > 3 {
		fields = fields[:3]
	}
	return strings.Join(fields, " ")
}

// CashflowLine is one category total of a single direction.
type CashflowLine struct {
	Type     string
	Category string
	Amount   decimal.Decimal
}

// FoldCashflow splits statement totals into income and consumption spend.
// Transfers, savings moves, and loan payments are neither income nor spend,
// so a large transfer cannot move the savings rate.
func FoldCashflow(lines []CashflowLine) (income, spend decimal.Decimal) {
	income = decimal.Zero
	spend = decimal.Zero
	for _, line := range lines {
		bucket := NormalizeStatementCategory(line.Category, "", line.Type)
		if strings.EqualFold(line.Type, entities.StatementTxnTypeCredit) {
			switch bucket {
			case BucketTransferIn, BucketTransferOut, BucketSavings, BucketLoan:
				continue
			}
			income = income.Add(line.Amount)
			continue
		}
		if IsConsumptionSpend(bucket) {
			spend = spend.Add(line.Amount)
		}
	}
	return income, spend
}

func titleShort(text string) string {
	fields := strings.Fields(text)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	out := strings.Trim(strings.Join(fields, " "), " -/")
	if len(out) > 80 {
		out = out[:80]
	}
	lower := strings.ToLower(out)
	if lower == "" {
		return ""
	}
	parts := strings.Fields(lower)
	for i, part := range parts {
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func onlyDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// AssignRecurrence marks repeated debit counterparties.
// Three or more lines with amounts within 5 percent of the median are a
// subscription. The same count with a varying amount is a bill.
func AssignRecurrence(txns []*entities.BankStatementTransaction) {
	type group struct {
		idx     []int
		amounts []float64
	}
	groups := map[string]*group{}
	for i, txn := range txns {
		if txn == nil || txn.Type != entities.StatementTxnTypeDebit {
			if txn != nil && txn.Recurrence == "" {
				txn.Recurrence = entities.StatementRecurrenceOneOff
			}
			continue
		}
		key := recurrenceKey(txn.Counterparty, txn.Description)
		if key == "" {
			txn.Recurrence = entities.StatementRecurrenceOneOff
			continue
		}
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		amt, _ := txn.Amount.Float64()
		g.idx = append(g.idx, i)
		g.amounts = append(g.amounts, amt)
	}
	for _, g := range groups {
		kind := entities.StatementRecurrenceOneOff
		if len(g.idx) >= 3 && amountsAreStable(g.amounts) {
			kind = entities.StatementRecurrenceSubscription
		} else if len(g.idx) >= 3 {
			kind = entities.StatementRecurrenceBill
		}
		for _, i := range g.idx {
			txns[i].Recurrence = kind
		}
	}
}

func amountsAreStable(amounts []float64) bool {
	if len(amounts) == 0 {
		return false
	}
	sorted := append([]float64(nil), amounts...)
	for i := 1; i < len(sorted); i++ {
		j := i
		for j > 0 && sorted[j] < sorted[j-1] {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			j--
		}
	}
	median := sorted[len(sorted)/2]
	if median <= 0 {
		return false
	}
	for _, amt := range amounts {
		delta := amt - median
		if delta < 0 {
			delta = -delta
		}
		if delta/median > 0.05 {
			return false
		}
	}
	return true
}

// ApplyCategoryRules lets a user correction replace the categorizer.
// Longer patterns win so "shoprite lekki" beats "shoprite".
func ApplyCategoryRules(txns []*entities.BankStatementTransaction, rules []entities.StatementCategoryRule) {
	if len(rules) == 0 {
		return
	}
	ordered := append([]entities.StatementCategoryRule(nil), rules...)
	for i := 1; i < len(ordered); i++ {
		j := i
		for j > 0 && len(ordered[j].Pattern) > len(ordered[j-1].Pattern) {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
			j--
		}
	}
	for _, txn := range txns {
		if txn == nil {
			continue
		}
		for _, rule := range ordered {
			if !ruleMatches(txn, rule) {
				continue
			}
			bucket := canonicalBucket(rule.Bucket)
			if bucket == "" {
				break
			}
			txn.Category = bucket
			txn.IsEssential = IsEssentialBucket(bucket)
			txn.CategoryConfidence = 0.99
			break
		}
	}
}

func ruleMatches(txn *entities.BankStatementTransaction, rule entities.StatementCategoryRule) bool {
	pattern := strings.ToLower(strings.TrimSpace(rule.Pattern))
	if pattern == "" {
		return false
	}
	desc := strings.ToLower(txn.Description)
	party := strings.ToLower(txn.Counterparty)
	switch strings.ToLower(rule.MatchType) {
	case entities.StatementRuleExact:
		return desc == pattern || party == pattern
	case entities.StatementRuleContains:
		return strings.Contains(desc, pattern) || strings.Contains(party, pattern)
	default:
		return false
	}
}

func inferBucket(description string) (bucket string, overrides bool) {
	text := strings.TrimSpace(description)
	if text == "" {
		return "", false
	}
	for _, rule := range narrationRules {
		if rule.re.MatchString(text) {
			return rule.bucket, rule.overrides
		}
	}
	return "", false
}

func directionFallback(txnType string) string {
	if strings.EqualFold(txnType, "credit") {
		return BucketTransferIn
	}
	return BucketOther
}

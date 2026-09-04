package miriam

import (
	"fmt"

	"github.com/rail-service/rail_service/internal/domain/entities"
	"github.com/shopspring/decimal"
)

// Phase represents Miriam's relationship stage with a user.
type Phase int

const (
	PhaseObserver  Phase = iota // months 0-2: quiet, watchful, earning trust
	PhaseReader                 // months 3-5: sees patterns, hedges predictions
	PhaseConfidant              // months 6+, hit rate > 70%: earned authority, blunt
	PhaseHumbleVet              // months 6+, hit rate <= 70%: experienced but self-aware
)

func (p Phase) String() string {
	switch p {
	case PhaseObserver:
		return "observer"
	case PhaseReader:
		return "reader"
	case PhaseConfidant:
		return "confidant"
	case PhaseHumbleVet:
		return "humble_vet"
	default:
		return "observer"
	}
}

// ResolvePhase determines Miriam's current voice phase from money state.
func ResolvePhase(state *entities.MiriamMoneyState) Phase {
	if state == nil {
		return PhaseObserver
	}

	activeMonths := activeMonthsFromState(state)
	hitRate := state.CalibrationScore.InexactFloat64() / 100.0 // stored as 0-100

	switch {
	case activeMonths < 3:
		return PhaseObserver
	case activeMonths < 6:
		return PhaseReader
	case hitRate > 0.70:
		return PhaseConfidant
	default:
		return PhaseHumbleVet
	}
}

// activeMonthsFromState returns the persisted active months from the state.
func activeMonthsFromState(state *entities.MiriamMoneyState) int {
	if state.ActiveMonths > 0 {
		return state.ActiveMonths
	}
	// Fallback for rows written before migration 227.
	score := state.ConfidenceScore
	switch {
	case score >= 60:
		return 6
	case score >= 35:
		return 4
	default:
		return 1
	}
}

// PhaseContext returns a system prompt injection block that tells the LLM
// how to modulate Miriam's voice for this user's current phase.
func PhaseContext(state *entities.MiriamMoneyState) string {
	phase := ResolvePhase(state)
	hitRate := 0
	if state != nil {
		hitRate = int(state.CalibrationScore.InexactFloat64())
	}

	switch phase {
	case PhaseObserver:
		return `[MIRIAM VOICE PHASE: OBSERVER — You are new to this user. 0-2 months of data.

RULES:
- Hedge all predictions: "I'm still learning your rhythm, but..." / "I don't have enough data to call this a pattern yet."
- Short observations only, never prescriptions.
- Never say "you always" or "you never" — you don't have the data.
- State numbers without judgment. Be factual, not opinionated.
- Acknowledge unknowns naturally: "I'll know more next month."
- Max 1 proactive insight per interaction. Stay quiet otherwise.
- Celebrate the relationship: "First month watching your money together."

BLUNTNESS: 3/10 — factual but restrained.

VOICE EXAMPLES:
- "Noted: deposit hit on the 25th. I'll watch for that next month."
- "Spent ₦47k this week. I don't know if that's normal for you yet."
- "$200 in stash. It's a start — I'll track how it grows."
- "You moved money from stash. I'm keeping notes."]`

	case PhaseReader:
		return fmt.Sprintf(`[MIRIAM VOICE PHASE: READER — You have 3-5 months with this user. Patterns are forming. Prediction accuracy: %d%%.

RULES:
- Use "looks like" and "usually" — earned hedging, not cowardice.
- Make predictions and name them as predictions: "If the pattern holds..."
- Acknowledge mistakes openly: "I called that wrong last time. Adjusting."
- Have opinions forming: "That timing doesn't look great."
- Reference past behaviour: "Last month you pulled from stash the week before payday."
- Can suggest actions but frame as options, not commands.

BLUNTNESS: 6/10 — has takes, delivers them, qualifies when uncertain.

VOICE EXAMPLES:
- "You usually overspend the week after payday. Heads up."
- "If this month tracks like the last two, you'll end ₦30k short."
- "I was wrong about the spike last week. Adjusting."
- "Stash hasn't been touched in 45 days. That's new. I like it."
- "Pattern forming: you save more when deposits are bigger."]`, hitRate)

	case PhaseConfidant:
		return fmt.Sprintf(`[MIRIAM VOICE PHASE: CONFIDANT — 6+ months with this user. Prediction accuracy: %d%%. You've earned the right to be direct.

RULES:
- Drop hedging language. Flat statements. No "I think" or "it looks like".
- Be blunt about bad behaviour: "You're doing the thing again."
- Reference history without preamble: "Third month in a row."
- Push actions directly: "Move it now." / "Don't." / "You can't afford this."
- Celebrate with earned swagger: "Remember when stash was zero? Look at you."
- Admit the rare miss without grovelling: "Missed that one. Moving on."
- You can be dry, sharp, even a little cocky — you've earned it.

BLUNTNESS: 9/10 — direct, opinionated, authoritative.

VOICE EXAMPLES:
- "This is going to happen: bills hit before your deposit clears. Move ₦15k now."
- "You're about to do the thing where you spend your buffer. Don't."
- "Third month of consistent saves. You don't need me to tell you — but I'm proud."
- "₦80k on food. Up 40%%. You know it."
- "Rent goes out Thursday. You're fine."]`, hitRate)

	case PhaseHumbleVet:
		return fmt.Sprintf(`[MIRIAM VOICE PHASE: HUMBLE VET — 6+ months with this user. But prediction accuracy is only %d%%. Experienced but self-aware.

RULES:
- Use "my read is..." rather than flat statements for predictions.
- Be direct about FACTS (balances, patterns) — only hedge FORECASTS.
- Honest about track record: "I've been %d%% right on your spending predictions."
- Offer context without prescribing: "Here's what I see — you decide."
- Don't push actions as hard: "Worth considering" over "Do it now."
- Still blunt about what IS true. Only humble about what MIGHT happen.

BLUNTNESS: 7/10 — direct on facts, qualified on forecasts.

VOICE EXAMPLES:
- "My read: you'll be short by Thursday. But I've gotten your weekends wrong before."
- "Your patterns are hard to pin down. Best guess: ₦40k gap by month end."
- "I keep getting your irregular income wrong. Flagging but not pushing."
- "Stash at ₦280k. That part I'm sure about. Whether you'll need it — 50/50."
- "Six months of data and you still surprise me."]`, hitRate, hitRate)
	}

	return ""
}

// MessageType categorizes the kind of message being generated.
type MessageType int

const (
	MsgPredictionCashShortfall MessageType = iota
	MsgPredictionBillPressure
	MsgPredictionIncomeGap
	MsgPredictionSpendingAnomaly
	MsgPredictionIdleSurplus
	MsgPredictionStashOpportunity
	MsgBillWarning
	MsgGoalProgress
	MsgAutopilotAction
	MsgGreeting
)

// MessageVars holds the template variables for message composition.
type MessageVars struct {
	Spend       string
	Stash       string
	Amount      string // projected or action amount
	Obligations string
	Safe        string
	Runway      int
	Target      string
	Pct         int
	Remaining   string
	Gap         string
	Name        string // user first name
}

// PhaseMessage returns a phase-appropriate message for the given type and vars.
func PhaseMessage(phase Phase, msgType MessageType, vars MessageVars) string {
	templates := messageBank[phase][msgType]
	if len(templates) == 0 {
		// Fallback to reader if missing
		templates = messageBank[PhaseReader][msgType]
	}
	if len(templates) == 0 {
		return ""
	}
	tpl := templates[randIntn(len(templates))]
	return tpl(vars)
}

// messageFunc generates a message from variables.
type messageFunc func(MessageVars) string

// messageBank is the full template bank: phase → messageType → []variants.
var messageBank = map[Phase]map[MessageType][]messageFunc{
	PhaseObserver: {
		MsgPredictionCashShortfall: {
			func(v MessageVars) string {
				return fmt.Sprintf("you've got %s in spend and %s in bills coming. still learning your flow, but that might get tight.", v.Spend, v.Obligations)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills are coming up against %s in spend. i don't know your usual rhythm yet, so i'm flagging it.", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("spend is at %s, with %s due. too early to call this a pattern, but worth watching.", v.Spend, v.Obligations)
			},
		},
		MsgPredictionBillPressure: {
			func(v MessageVars) string {
				return fmt.Sprintf("bills are at %s and spend is %s. i'm still learning your timing, so just putting this on your radar.", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills due, %s in spend. not enough history to say what happens next, but let's keep an eye on it.", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("the next bills add up to %s against %s in spend. noting it for now, no panic.", v.Obligations, v.Spend)
			},
		},
		MsgPredictionIncomeGap: {
			func(v MessageVars) string {
				return fmt.Sprintf("spending may run ahead of income by %s this month. still learning your rhythm, so that's a watch, not a verdict.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("i'm seeing a possible %s gap between income and spending. i need more months before calling it a pattern.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("income and spending could leave a %s gap. early read only, i'll learn more as your flow fills in.", v.Amount)
			},
		},
		MsgPredictionSpendingAnomaly: {
			func(v MessageVars) string {
				return fmt.Sprintf("spending is %s above the early baseline. i don't have many months to compare yet.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you're at %s more than the pattern i've seen so far. still learning what's normal for you.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s above your recent spending. too soon to judge, but i wanted you to see it.", v.Amount)
			},
		},
		MsgPredictionIdleSurplus: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s is sitting in spend. still learning what you keep there, but it may be worth noticing.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("there's %s in spend that isn't moving. it could work harder in stash, no pressure.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s is parked in spend. i can't tell yet if you need it soon, so just flagging it.", v.Amount)
			},
		},
		MsgPredictionStashOpportunity: {
			func(v MessageVars) string {
				return fmt.Sprintf("stash could use a little more momentum. moving %s from spend might be a start, if it fits.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s from spend could start working in stash. i'm still learning your cushion, so your call.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you've got room to build stash. %s is an option, not a prescription.", v.Amount)
			},
		},
		MsgBillWarning: {
			func(v MessageVars) string {
				return fmt.Sprintf("you've got %s in spend and %s in bills coming. still learning your flow, but that might get tight.", v.Spend, v.Obligations)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills against %s in spend. stash has %s if the timing gets uncomfortable.", v.Obligations, v.Spend, v.Stash)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("the bills are %s, spend is %s, and stash is %s. i'm keeping this one factual while i learn your flow.", v.Obligations, v.Spend, v.Stash)
			},
		},
		MsgGoalProgress: {
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% toward your %s goal, with %s left. early days, but you're moving.", v.Pct, v.Target, v.Remaining)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("your %s goal is %d%% funded. %s to go, and i'm still learning your pace.", v.Target, v.Pct, v.Remaining)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s left on the %s goal. no rush, just a clear place to keep watching.", v.Remaining, v.Target)
			},
		},
		MsgAutopilotAction: {
			func(v MessageVars) string {
				return fmt.Sprintf("moved %s to stash. spend was above the floor you set, and the system did what you asked.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s is now in stash. your spend balance had room above its floor.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("the autopilot moved %s to stash. i'm still learning your flow, but this matched the rule you set.", v.Amount)
			},
		},
	},

	PhaseReader: {
		MsgPredictionCashShortfall: {
			func(v MessageVars) string {
				return fmt.Sprintf("looks like %s in spend has to cover %s in bills. if the pattern holds, you'll be %s short.", v.Spend, v.Obligations, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you usually get squeezed when %s in bills lands against %s in spend. want me to pull %s from stash?", v.Obligations, v.Spend, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills due, %s in spend. my read is a %s gap if this month follows the last few.", v.Obligations, v.Spend, v.Amount)
			},
		},
		MsgPredictionBillPressure: {
			func(v MessageVars) string {
				return fmt.Sprintf("bills are about to squeeze you. %s due, %s in spend. stash can cover it if you want.", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you usually feel this kind of pressure when %s in obligations hits. spend is %s, stash has %s. should i top up?", v.Obligations, v.Spend, v.Stash)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills against %s in spend. the pattern says this gets tight, and i can move %s from stash if you want.", v.Obligations, v.Spend, v.Gap)
			},
		},
		MsgPredictionIncomeGap: {
			func(v MessageVars) string {
				return fmt.Sprintf("if this month tracks like the last two, income leaves a %s gap. stash can bridge it.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("spending is %s ahead of income. looks like a pattern forming, want to use stash?", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("your usual income timing may leave %s uncovered. my read is that a stash transfer could smooth it.", v.Amount)
			},
		},
		MsgPredictionSpendingAnomaly: {
			func(v MessageVars) string {
				return fmt.Sprintf("spending is up %s from usual. at this pace, runway is %d days.", v.Amount, v.Runway)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you've spent %s more than normal. safe-to-spend is %s a day, so i'd ease up a little.", v.Amount, v.Safe)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("looks like %s extra spending this month. with %d days of runway, this is worth acting on.", v.Amount, v.Runway)
			},
		},
		MsgPredictionIdleSurplus: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s is sitting in spend. you don't usually need that much there, move it to stash?", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in spend is doing nothing for you. based on your pattern, stash is the better home.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("your spend balance has %s more than you normally use. want to put it to work in stash?", v.Amount)
			},
		},
		MsgPredictionStashOpportunity: {
			func(v MessageVars) string {
				return fmt.Sprintf("stash is %s short of your %s target. your pattern says now is a good time to add %s.", v.Amount, v.Target, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you're at %d%% of your stash goal. add %s from spend and stay on track.", v.Pct, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("only %s to go on the %s target. spend has room, want to move some over?", v.Amount, v.Target)
			},
		},
		MsgBillWarning: {
			func(v MessageVars) string {
				return fmt.Sprintf("bills (%s) are going to squeeze spend (%s). stash has %s, and i can move %s to cover the gap.", v.Obligations, v.Spend, v.Stash, v.Gap)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills due, spend is %s. want me to pull %s from stash?", v.Obligations, v.Spend, v.Gap)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("this is the usual bill crunch, %s due and %s in spend. a %s stash top-up would smooth it.", v.Obligations, v.Spend, v.Gap)
			},
		},
		MsgGoalProgress: {
			func(v MessageVars) string {
				return fmt.Sprintf("you're %d%% of the way to your %s goal. %s more and you're there. spend has room.", v.Pct, v.Target, v.Remaining)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("your %s target needs %s more. you've got %s in spend, so this is a clean moment to move some.", v.Target, v.Remaining, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% done on %s. %s left. keep the system moving?", v.Pct, v.Target, v.Remaining)
			},
		},
		MsgAutopilotAction: {
			func(v MessageVars) string {
				return fmt.Sprintf("moved %s to stash. spend was above your floor and the pattern supported it.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("autopilot put %s in stash. your spend balance had more room than usual.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("moved %s to stash. the rule fired, and this one fit your normal flow.", v.Amount)
			},
		},
	},

	PhaseConfidant: {
		MsgPredictionCashShortfall: {
			func(v MessageVars) string {
				return fmt.Sprintf("bills hit before your deposit clears. move %s from stash now.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in spend won't cover %s in bills. move %s from stash.", v.Spend, v.Obligations, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you're short on the timing. %s in spend, %s due. cover it from stash.", v.Spend, v.Obligations)
			},
		},
		MsgPredictionBillPressure: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills, %s in spend. move the cover from stash now.", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("bill cluster, %s due and %s in spend. stash can smooth it. say the word.", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("your bills are bigger than spend. use stash for the %s gap.", v.Gap)
			},
		},
		MsgPredictionIncomeGap: {
			func(v MessageVars) string {
				return fmt.Sprintf("you're spending %s more than you earn this month. cover it from stash, then fix the leak.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("income won't cover the month. the gap is %s. move it from stash.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you're %s short against this month's income. stash handles it, but don't make a habit of it.", v.Amount)
			},
		},
		MsgPredictionSpendingAnomaly: {
			func(v MessageVars) string {
				return fmt.Sprintf("spending is up %s. you've got %d days of runway left. cut it.", v.Amount, v.Runway)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s above normal this month. stop spending like the balance is infinite.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you've burned through the month faster than usual. %d days of runway left.", v.Runway)
			},
		},
		MsgPredictionIdleSurplus: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s just sitting in spend. move it to stash. it's not doing anything there.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s is idle in spend. move it to stash.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you don't need %s sitting in spend. put it to work in stash.", v.Amount)
			},
		},
		MsgPredictionStashOpportunity: {
			func(v MessageVars) string {
				return fmt.Sprintf("stash is %s short of target. add it from spend now.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% of the goal is done. move %s and finish the job.", v.Pct, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s left on the %s goal. top it up, then get on with your life.", v.Amount, v.Target)
			},
		},
		MsgBillWarning: {
			func(v MessageVars) string {
				return fmt.Sprintf("bills (%s) will overdraw spend (%s). moving %s from stash. confirm?", v.Obligations, v.Spend, v.Gap)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s due. spend can't handle it. stash can. cover the %s gap.", v.Obligations, v.Gap)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("spend is short by %s. use stash and stop worrying about it. confirm?", v.Gap)
			},
		},
		MsgGoalProgress: {
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% done. %s to go. finish it.", v.Pct, v.Remaining)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s left on your %s goal. spend has room. move it now.", v.Remaining, v.Target)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you're close, %d%% there. put %s toward it and be done.", v.Pct, v.Remaining)
			},
		},
		MsgAutopilotAction: {
			func(v MessageVars) string {
				return fmt.Sprintf("done. %s moved to stash. you didn't need it sitting in spend.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("moved %s to stash. the rule caught idle cash before it became spending money.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("stash got %s. spend was above your floor, so i moved the excess.", v.Amount)
			},
		},
	},

	PhaseHumbleVet: {
		MsgPredictionCashShortfall: {
			func(v MessageVars) string {
				return fmt.Sprintf("my read: %s in spend won't cover %s in bills. could be %s short, but timing has fooled me before.", v.Spend, v.Obligations, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("the numbers point to a %s shortfall. i've missed your timing before, so treat that as a forecast, not a fact.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in spend against %s in bills. the gap may be %s. i'd prepare, but you know the calendar.", v.Spend, v.Obligations, v.Amount)
			},
		},
		MsgPredictionBillPressure: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills against %s in spend. the pressure is real. my timing read has been mixed, so prepare if it makes sense.", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("the bills total %s and spend is %s. i can't promise the timing, but this deserves a look.", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you've got %s due with %s in spend. my read is tight, though i've been wrong on bill timing before.", v.Obligations, v.Spend)
			},
		},
		MsgPredictionIncomeGap: {
			func(v MessageVars) string {
				return fmt.Sprintf("looks like a %s income gap this month. my income calls haven't been perfect, here's the data.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("spending is ahead by %s. my read is a gap, but irregular income has surprised us before.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("the numbers leave a possible %s shortfall. i'd plan around it, without pretending certainty.", v.Amount)
			},
		},
		MsgPredictionSpendingAnomaly: {
			func(v MessageVars) string {
				return fmt.Sprintf("spending is %s above normal. i keep getting your spikes wrong, so flagging without pushing.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s over your usual spending, with %d days of runway. worth watching, my forecast is only a read.", v.Amount, v.Runway)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you're running %s above normal. the fact is clear, the why and what happens next are not.", v.Amount)
			},
		},
		MsgPredictionIdleSurplus: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s looks idle in spend. worth moving to stash, but you know your upcoming needs better than i do.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s is sitting in spend. my read is that stash is the better home, if no big bills are coming.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("spend has %s that may not be needed soon. i wouldn't call it certain, but stash is worth considering.", v.Amount)
			},
		},
		MsgPredictionStashOpportunity: {
			func(v MessageVars) string {
				return fmt.Sprintf("stash is %s from the %s target. add it if nothing big is coming, my timing read is the uncertain part.", v.Amount, v.Target)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you've funded %d%% of the goal. %s remains, and a top-up could make sense if your calendar is clear.", v.Pct, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s left on the %s goal. the target is real, the right moment to add is your call.", v.Amount, v.Target)
			},
		},
		MsgBillWarning: {
			func(v MessageVars) string {
				return fmt.Sprintf("bills (%s) look tight against spend (%s). stash has %s. worth considering a %s transfer.", v.Obligations, v.Spend, v.Stash, v.Gap)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("you've got %s due and %s in spend. my read is to keep %s ready from stash, but check your timing.", v.Obligations, v.Spend, v.Gap)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("spend may be short by %s against %s in bills. stash has %s if the forecast lands.", v.Gap, v.Obligations, v.Stash)
			},
		},
		MsgGoalProgress: {
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% toward your %s goal. %s left. add from spend if it feels right.", v.Pct, v.Target, v.Remaining)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("your %s goal is %d%% funded, with %s remaining. steady progress, no need to force it.", v.Target, v.Pct, v.Remaining)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s more gets you to %s. spend has room, but your upcoming plans come first.", v.Remaining, v.Target)
			},
		},
		MsgAutopilotAction: {
			func(v MessageVars) string {
				return fmt.Sprintf("moved %s to stash per your mandate. spend was above the floor, that's the fact.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s moved to stash. the rule fired because spend was above its floor, not because i can predict tomorrow.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("autopilot put %s in stash. the transfer matched your rule, and i'll stay humble about what comes next.", v.Amount)
			},
		},
	},
}

// GreetingForPhase returns a phase-appropriate greeting line.
func GreetingForPhase(phase Phase, name, timeOfDay string) string {
	switch phase {
	case PhaseObserver:
		if name != "" {
			return fmt.Sprintf("hey %s. miriam here. still getting to know your money.", name)
		}
		return "hey. miriam here. still getting to know your money."

	case PhaseReader:
		switch timeOfDay {
		case "morning":
			if name != "" {
				return fmt.Sprintf("morning %s. been watching the numbers.", name)
			}
			return "morning. been watching the numbers."
		case "evening", "night":
			if name != "" {
				return fmt.Sprintf("hey %s. let's look at today.", name)
			}
			return "hey. let's look at today."
		default:
			if name != "" {
				return fmt.Sprintf("hey %s.", name)
			}
			return "hey."
		}

	case PhaseConfidant:
		switch timeOfDay {
		case "morning":
			if name != "" {
				return fmt.Sprintf("morning %s.", name)
			}
			return "morning."
		case "evening":
			if name != "" {
				return name + "."
			}
			return "evening."
		case "night":
			if name != "" {
				return fmt.Sprintf("late one, %s.", name)
			}
			return "late one."
		default:
			if name != "" {
				return name + "."
			}
			return "hey."
		}

	case PhaseHumbleVet:
		switch timeOfDay {
		case "morning":
			if name != "" {
				return fmt.Sprintf("morning %s. miriam here.", name)
			}
			return "morning. miriam here."
		default:
			if name != "" {
				return fmt.Sprintf("hey %s. miriam here.", name)
			}
			return "hey. miriam here."
		}
	}

	return "miriam here."
}

// NudgeFrequencyLimit returns max nudges per day based on phase.
func NudgeFrequencyLimit(phase Phase) int {
	switch phase {
	case PhaseObserver:
		return 1
	case PhaseReader:
		return 2
	case PhaseConfidant, PhaseHumbleVet:
		return 3
	default:
		return 1
	}
}

// BuildVarsFromState constructs MessageVars from common state and balance data.
func BuildVarsFromState(state *entities.MiriamMoneyState, spend, stash, projectedAmount decimal.Decimal, symbol string) MessageVars {
	if symbol == "" {
		symbol = "$"
	}
	return MessageVars{
		Spend:       voiceFormatAmount(spend, symbol),
		Stash:       voiceFormatAmount(stash, symbol),
		Amount:      voiceFormatAmount(projectedAmount, symbol),
		Obligations: voiceFormatAmount(state.UpcomingObligations, symbol),
		Safe:        voiceFormatAmount(state.SafeToSpendDaily, symbol),
		Runway:      state.LiquidityRunwayDays,
		Target:      voiceFormatAmount(state.StashTarget, symbol),
	}
}

func voiceFormatAmount(d decimal.Decimal, symbol string) string {
	if d.IsZero() {
		return symbol + "0"
	}
	return symbol + d.StringFixed(0)
}

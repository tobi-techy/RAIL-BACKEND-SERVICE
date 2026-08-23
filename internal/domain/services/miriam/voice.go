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
				return fmt.Sprintf("Spend is at %s with %s in bills due. I'm still learning your flow — worth keeping an eye on.", v.Spend, v.Obligations)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("I see %s in bills coming. Spend has %s. I don't know your pattern yet, but flagging it.", v.Obligations, v.Spend)
			},
		},
		MsgPredictionBillPressure: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills due. Spend at %s. Noting this for next time.", v.Obligations, v.Spend)
			},
		},
		MsgPredictionIncomeGap: {
			func(v MessageVars) string {
				return fmt.Sprintf("Spending looks like it'll outpace income by %s this month. Still learning your rhythm though.", v.Amount)
			},
		},
		MsgPredictionSpendingAnomaly: {
			func(v MessageVars) string {
				return fmt.Sprintf("Spending is %s above what I'd expect. I don't have many months to compare yet.", v.Amount)
			},
		},
		MsgPredictionIdleSurplus: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s sitting in Spend doing nothing. That could be earning yield in Stash right now.", v.Amount)
			},
		},
		MsgPredictionStashOpportunity: {
			func(v MessageVars) string {
				return fmt.Sprintf("Your Stash has room to grow. %s from Spend could start working for you — no pressure.", v.Amount)
			},
		},
		MsgBillWarning: {
			func(v MessageVars) string {
				return fmt.Sprintf("Bills (%s) look tight against Spend (%s). Stash has %s if needed.", v.Obligations, v.Spend, v.Stash)
			},
		},
		MsgGoalProgress: {
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% toward your %s goal. %s to go. Early days.", v.Pct, v.Target, v.Remaining)
			},
		},
		MsgAutopilotAction: {
			func(v MessageVars) string {
				return fmt.Sprintf("Moved %s to Stash. Spend was above the floor you set.", v.Amount)
			},
		},
	},

	PhaseReader: {
		MsgPredictionCashShortfall: {
			func(v MessageVars) string {
				return fmt.Sprintf("Spend is at %s with %s in bills due. If the pattern holds, you'll be %s short. Stash can cover.", v.Spend, v.Obligations, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("Looks like %s in bills vs %s in Spend. Usually that means a gap. Want me to pull from Stash?", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("Based on the last few months, %s in Spend won't cover %s in bills. Move %s from Stash?", v.Spend, v.Obligations, v.Amount)
			},
		},
		MsgPredictionBillPressure: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills this week vs %s in Spend. Pattern says this gets tight. Cover from Stash?", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("Your %s in obligations usually crowds Spend (%s). Stash has %s — should I top up?", v.Obligations, v.Spend, v.Stash)
			},
		},
		MsgPredictionIncomeGap: {
			func(v MessageVars) string {
				return fmt.Sprintf("If this month tracks like the last two, income won't cover expenses by %s. Stash can bridge it.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("Spending is %s more than income this month. Looks like a pattern forming. Pull from Stash?", v.Amount)
			},
		},
		MsgPredictionSpendingAnomaly: {
			func(v MessageVars) string {
				return fmt.Sprintf("Spending is up %s vs your usual. At this pace, runway is %d days.", v.Amount, v.Runway)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("You've spent %s more than normal this month. Safe-to-spend is %s/day — might want to ease up.", v.Amount, v.Safe)
			},
		},
		MsgPredictionIdleSurplus: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s sitting idle in Spend. You don't usually need that much. Move to Stash?", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in Spend isn't doing anything. Based on your pattern, it can go to Stash.", v.Amount)
			},
		},
		MsgPredictionStashOpportunity: {
			func(v MessageVars) string {
				return fmt.Sprintf("Stash (%s) is %s short of your %s target. Pattern says now is a good time to add.", v.Stash, v.Amount, v.Target)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("You're at %d%% of your Stash goal. Add %s from Spend to stay on track.", v.Pct, v.Amount)
			},
		},
		MsgBillWarning: {
			func(v MessageVars) string {
				return fmt.Sprintf("Bills (%s) are going to squeeze Spend (%s). Stash has %s. I can move %s to cover the gap.", v.Obligations, v.Spend, v.Stash, v.Gap)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills due. Spend won't cover it at %s. Want me to pull %s from Stash?", v.Obligations, v.Spend, v.Gap)
			},
		},
		MsgGoalProgress: {
			func(v MessageVars) string {
				return fmt.Sprintf("You're %d%% of the way to your %s goal. Need %s more. Spend has room.", v.Pct, v.Target, v.Remaining)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("Need %s more for your %s target. Spend has %s — tap to move it.", v.Remaining, v.Target, v.Spend)
			},
		},
		MsgAutopilotAction: {
			func(v MessageVars) string {
				return fmt.Sprintf("Moved %s to Stash. Spend was above your floor and the pattern looked right.", v.Amount)
			},
		},
	},

	PhaseConfidant: {
		MsgPredictionCashShortfall: {
			func(v MessageVars) string {
				return fmt.Sprintf("Bills hit before your deposit clears. Move %s from Stash now.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s in Spend, %s in bills. You're short. I can pull %s from Stash.", v.Spend, v.Obligations, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("Paycheck's not here yet. %s won't cover %s. Let me fill the gap.", v.Spend, v.Obligations)
			},
		},
		MsgPredictionBillPressure: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills. %s in Spend. Do the math. Auto-cover from Stash?", v.Obligations, v.Spend)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("Bill cluster: %s due, %s in Spend. Stash can smooth it. Say the word.", v.Obligations, v.Spend)
			},
		},
		MsgPredictionIncomeGap: {
			func(v MessageVars) string {
				return fmt.Sprintf("You're spending %s more than you earn this month. Stash covers it. Move?", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("Income won't cover this month. %s gap. I've got it from Stash if you say go.", v.Amount)
			},
		},
		MsgPredictionSpendingAnomaly: {
			func(v MessageVars) string {
				return fmt.Sprintf("Spending is up %s. You know it. %d days of runway left.", v.Amount, v.Runway)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s above normal this month. I'm watching it.", v.Amount)
			},
		},
		MsgPredictionIdleSurplus: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s idle in Spend. Put it to work. Stash.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s doing nothing. Move it.", v.Amount)
			},
		},
		MsgPredictionStashOpportunity: {
			func(v MessageVars) string {
				return fmt.Sprintf("Stash is %s short. Add it. You won't miss it from Spend.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% of goal done. %s to go. Top it up.", v.Pct, v.Amount)
			},
		},
		MsgBillWarning: {
			func(v MessageVars) string {
				return fmt.Sprintf("Bills (%s) will overdraw Spend (%s). Moving %s from Stash. Confirm?", v.Obligations, v.Spend, v.Gap)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s due. Spend can't handle it. Stash can. Let me cover the %s gap.", v.Obligations, v.Gap)
			},
		},
		MsgGoalProgress: {
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% done. %s to go. You're close. Finish it.", v.Pct, v.Remaining)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s left on your goal. Spend has room. Do it now.", v.Remaining)
			},
		},
		MsgAutopilotAction: {
			func(v MessageVars) string {
				return fmt.Sprintf("Done. %s moved to Stash. You didn't need it sitting there.", v.Amount)
			},
		},
	},

	PhaseHumbleVet: {
		MsgPredictionCashShortfall: {
			func(v MessageVars) string {
				return fmt.Sprintf("My read: %s in Spend won't cover %s in bills. Could be %s short. Stash can bridge it if I'm right.", v.Spend, v.Obligations, v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("I think you'll be %s short. I've been wrong before on timing, but the gap looks real.", v.Amount)
			},
		},
		MsgPredictionBillPressure: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s in bills vs %s in Spend. My track record on bill timing is mixed, but worth preparing.", v.Obligations, v.Spend)
			},
		},
		MsgPredictionIncomeGap: {
			func(v MessageVars) string {
				return fmt.Sprintf("Looks like a %s income gap this month. My income predictions haven't been perfect — here's the data, you decide.", v.Amount)
			},
		},
		MsgPredictionSpendingAnomaly: {
			func(v MessageVars) string {
				return fmt.Sprintf("Spending is %s above normal. I keep getting your spikes wrong, so flagging without pushing.", v.Amount)
			},
			func(v MessageVars) string {
				return fmt.Sprintf("%s over normal spending. Runway at %d days. Worth watching.", v.Amount, v.Runway)
			},
		},
		MsgPredictionIdleSurplus: {
			func(v MessageVars) string {
				return fmt.Sprintf("%s looks idle in Spend. Worth moving to Stash — but you know your upcoming needs better than I do.", v.Amount)
			},
		},
		MsgPredictionStashOpportunity: {
			func(v MessageVars) string {
				return fmt.Sprintf("Stash is %s from target. Good time to add if nothing big is coming.", v.Amount)
			},
		},
		MsgBillWarning: {
			func(v MessageVars) string {
				return fmt.Sprintf("Bills (%s) look tight against Spend (%s). Stash has %s. Worth considering a %s transfer.", v.Obligations, v.Spend, v.Stash, v.Gap)
			},
		},
		MsgGoalProgress: {
			func(v MessageVars) string {
				return fmt.Sprintf("%d%% toward your %s goal. %s left. Add from Spend if it feels right.", v.Pct, v.Target, v.Remaining)
			},
		},
		MsgAutopilotAction: {
			func(v MessageVars) string {
				return fmt.Sprintf("Moved %s to Stash per your mandate. Spend was above the floor.", v.Amount)
			},
		},
	},
}

// GreetingForPhase returns a phase-appropriate greeting line.
func GreetingForPhase(phase Phase, name, timeOfDay string) string {
	switch phase {
	case PhaseObserver:
		if name != "" {
			return fmt.Sprintf("Hey %s. Miriam. Still getting to know your money.", name)
		}
		return "Hey. Miriam. Still getting to know your money."

	case PhaseReader:
		switch timeOfDay {
		case "morning":
			if name != "" {
				return fmt.Sprintf("Morning %s. Miriam. I've been watching the numbers.", name)
			}
			return "Morning. Miriam. I've been watching the numbers."
		case "evening", "night":
			if name != "" {
				return fmt.Sprintf("%s. Miriam. Let's look at today.", name)
			}
			return "Miriam. Let's look at today."
		default:
			if name != "" {
				return fmt.Sprintf("Hey %s. Miriam.", name)
			}
			return "Hey. Miriam."
		}

	case PhaseConfidant:
		switch timeOfDay {
		case "morning":
			if name != "" {
				return fmt.Sprintf("Morning %s.", name)
			}
			return "Morning."
		case "evening":
			if name != "" {
				return name + ". Miriam."
			}
			return "Miriam."
		case "night":
			if name != "" {
				return fmt.Sprintf("Late one, %s.", name)
			}
			return "Late one."
		default:
			if name != "" {
				return name + "."
			}
			return "Miriam."
		}

	case PhaseHumbleVet:
		switch timeOfDay {
		case "morning":
			if name != "" {
				return fmt.Sprintf("Morning %s. Miriam here.", name)
			}
			return "Morning. Miriam here."
		default:
			if name != "" {
				return fmt.Sprintf("Hey %s. Miriam.", name)
			}
			return "Hey. Miriam."
		}
	}

	return "Miriam."
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

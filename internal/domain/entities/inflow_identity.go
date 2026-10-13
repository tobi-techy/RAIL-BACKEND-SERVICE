package entities

// Rails that report an inflow to Miriam's ledger. The value travels as the
// inflow's `channel`, so it is the same string for every credit that arrives
// through that rail.
const (
	InflowChannelBridge = "bridge"
	InflowChannelGraph  = "graph"
	InflowChannelChain  = "chain"
	InflowChannelP2P    = "p2p"
)

// InflowIdentity is the structured payment identity the rail hands to Miriam's
// ledger alongside payment_id. Miriam dedupes on (channel, sender, amount,
// reference), so the same transfer reported twice, or worded differently, still
// counts once.
//
// Every field is filled only from what the provider actually reported. An empty
// field means the provider did not supply it; it is never derived, defaulted, or
// copied from payment_id, because an invented value would let a payment look
// verified when nothing verified it.
type InflowIdentity struct {
	Channel   string
	Sender    string
	Reference string
}

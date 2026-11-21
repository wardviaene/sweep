package transcribe

// Prompt kinds, by band.
const (
	KindAirband = "airband"
	KindHam     = "ham"
	KindFRS     = "frs"
	KindMarine  = "marine"
	KindNOAA    = "noaa"
	KindGeneric = "generic"
)

var prompts = map[string]string{
	KindAirband: "Aviation radio. Callsigns, runway, heading, altitude, flight level, squawk, contact, cleared, niner, tree, fife, Cessna, Piper, Skyhawk, tower, ground, approach, traffic, downwind, base, final.",
	KindHam:     "Amateur radio. Callsign, CQ, QSO, repeater, 73, signal report, five nine, over, this is, clear, net control, antenna.",
	KindFRS:     "Family radio. Channel, copy, over, roger, come in, ten four, base, are you there, meet at, on my way.",
	KindMarine:  "Marine VHF radio. Mayday, pan-pan, securite, channel one six, vessel, coast guard, over, out, this is, position, nautical miles, anchorage, bridge, lock.",
	KindNOAA:    "National Weather Service forecast. Mostly sunny, chance of rain, winds, knots, gusts, wave heights, small craft advisory, high temperature, low temperature, percent.",
	KindGeneric: "Two-way radio traffic. Copy, roger, over, out, say again, stand by, affirmative, negative.",
}

// BandKind picks the prompt kind for a frequency.
func BandKind(freqHz uint32) string {
	f := float64(freqHz) / 1e6
	switch {
	case f >= 162.4 && f <= 162.56:
		return KindNOAA
	case f >= 108 && f <= 137:
		return KindAirband
	case f >= 156 && f <= 162.025, f >= 160.6 && f <= 162.0:
		return KindMarine
	case f >= 446 && f <= 446.2, f >= 462 && f <= 468, f >= 149.8 && f <= 151:
		return KindFRS
	case f >= 1.8 && f <= 29.7, f >= 50 && f <= 54, f >= 144 && f <= 148, f >= 222 && f <= 225, f >= 420 && f <= 450, f >= 902 && f <= 928:
		return KindHam
	}
	return KindGeneric
}

// PromptFor returns the initial prompt for a frequency.
func PromptFor(freqHz uint32) (kind, prompt string) {
	kind = BandKind(freqHz)
	return kind, prompts[kind]
}

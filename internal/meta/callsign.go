package meta

import "regexp"

var (
	// airline callsign: 3-letter ICAO designator + flight number (digits, up to two trailing letters)
	airlineCallsign = regexp.MustCompile(`^([A-Z]{3})([0-9]{1,4}[A-Z]{0,2})$`)
	// US registration: N + 1-5 digits, or digits followed by 1-2 letters (no I, O; never starts with 0)
	usReg = regexp.MustCompile(`^N[1-9]([0-9]{0,4}|[0-9]{0,3}[A-HJ-NP-Z]|[0-9]{0,2}[A-HJ-NP-Z]{2})$`)
)

// SplitCallsign splits "UAL123" into ("UAL", "123"). ok is false for anything
// that is not designator+number (registrations, short or odd callsigns).
func SplitCallsign(cs string) (icao, number string, ok bool) {
	m := airlineCallsign.FindStringSubmatch(cs)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// IsRegistration reports whether cs looks like a US N-number.
func IsRegistration(cs string) bool { return usReg.MatchString(cs) }

package transcribe

import (
	"regexp"
	"strings"
	"unicode"
)

// hallucinations are what whisper typically emits on noise, as normalized
// text (lower case, letters and spaces only).
var hallucinations = map[string]bool{
	"thank you": true, "thanks": true, "thank you very much": true, "thank you so much": true,
	"thanks for watching": true, "thank you for watching": true, "thank you for watching the video": true,
	"you": true, "bye": true, "bye bye": true, "goodbye": true, "okay": true, "so": true, "the": true,
	"yeah": true, "oh": true, "uh": true, "um": true, "hmm": true, "mm": true, "mmm": true, "huh": true,
	"the end": true, "see you next time": true, "see you in the next video": true,
	"please subscribe": true, "subscribe": true, "like and subscribe": true,
	"i m sorry": true, "i'm sorry": true,
}

var hallucinationParts = []string{"amara org", "subtitles by", "subtitled by", "captions by", "transcribed by", "www ", "http"}

var tagRE = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|\*[^*]*\*|[♪♫]+`)

// normalize lower-cases s and keeps letters, digits and single spaces.
func normalize(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'':
			b.WriteRune(r)
			space = false
		case !space:
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// Filter cleans whisper output for a clip of durationS seconds. It removes
// bracketed sound tags and reports empty when what is left is a known
// hallucination, a repetition loop, or implausibly little text for the length.
func Filter(text string, durationS float64) (clean string, empty bool) {
	t := strings.TrimSpace(tagRE.ReplaceAllString(text, " "))
	t = strings.Join(strings.Fields(t), " ")
	n := normalize(t)
	if n == "" {
		return "", true
	}
	if hallucinations[n] {
		return "", true
	}
	for _, p := range hallucinationParts {
		if strings.Contains(n+" ", p) {
			return "", true
		}
	}
	words := strings.Fields(n)
	// repetition loops: few distinct words in a long run, or one n-gram on repeat
	if len(words) >= 4 {
		seen := map[string]bool{}
		for _, w := range words {
			seen[w] = true
		}
		if float64(len(seen))/float64(len(words)) <= 0.34 {
			return "", true
		}
	}
	if repeatsNgram(words) {
		return "", true
	}
	// text per second: speech is about 10-15 characters per second
	if durationS >= 3 && float64(len(n))/durationS < 0.4 {
		return "", true
	}
	return t, false
}

// repeatsNgram reports a 1..4-word phrase that repeats back to back 3+ times
// and covers most of the text ("Thank you. Thank you. Thank you.").
func repeatsNgram(words []string) bool {
	for n := 1; n <= 4; n++ {
		for start := 0; start+3*n <= len(words); start++ {
			reps := 1
			for p := start + n; p+n <= len(words) && equal(words[start:start+n], words[p:p+n]); p += n {
				reps++
			}
			if reps >= 3 && reps*n*10 >= len(words)*6 {
				return true
			}
		}
	}
	return false
}

func equal(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

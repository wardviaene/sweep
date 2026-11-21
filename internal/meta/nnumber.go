package meta

import "strconv"

// US N-numbers are allocated algorithmically inside the US ICAO block:
// A00001 = N1 ... ADF7C7 = N99999. This is the standard conversion.

const (
	nCharset   = "ABCDEFGHJKLMNPQRSTUVWXYZ" // letters used in N-numbers (no I, no O)
	nSuffix    = 601                        // "", A..Z, AA..ZZ
	nBucket4   = 35                         // after 4 digits: "", 24 letters, 10 digits
	nBucket3   = 10*nBucket4 + nSuffix      // 951
	nBucket2   = 10*nBucket3 + nSuffix      // 10111
	nBucket1   = 10*nBucket2 + nSuffix      // 101711
	nFirstHex  = 0xA00001
	nCount     = 9*nBucket1 + 0 // number of N-numbers (915399)
	nLastHexIn = nFirstHex + nCount - 1
)

// NNumberFromHex converts a US Mode S address (A00001..ADF7C7) to its
// N-number ("N123AB"). ok is false outside that range.
func NNumberFromHex(hex uint32) (string, bool) {
	if hex < nFirstHex || hex > nLastHexIn {
		return "", false
	}
	i := int(hex - nFirstHex)
	out := []byte("N")
	out = strconv.AppendInt(out, int64(i/nBucket1+1), 10)
	i %= nBucket1
	if i < nSuffix {
		return string(append(out, nSuffixStr(i)...)), true
	}
	i -= nSuffix
	for _, bucket := range []int{nBucket2, nBucket3} {
		out = strconv.AppendInt(out, int64(i/bucket), 10)
		i %= bucket
		if i < nSuffix {
			return string(append(out, nSuffixStr(i)...)), true
		}
		i -= nSuffix
	}
	out = strconv.AppendInt(out, int64(i/nBucket4), 10)
	i %= nBucket4
	switch {
	case i == 0:
	case i <= len(nCharset):
		out = append(out, nCharset[i-1])
	default:
		out = strconv.AppendInt(out, int64(i-len(nCharset)-1), 10)
	}
	return string(out), true
}

// nSuffixStr maps 0..600 to "", "A", "AA", "AB" ... "Z", "ZZ" (up to two letters).
func nSuffixStr(i int) string {
	if i == 0 {
		return ""
	}
	i--
	c1 := nCharset[i/(len(nCharset)+1)]
	if r := i % (len(nCharset) + 1); r > 0 {
		return string([]byte{c1, nCharset[r-1]})
	}
	return string(c1)
}

package agent

// TruncateStr truncates a string to n runes.
// Exported for network subpackage access.
func TruncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}

	limit := n
	if limit > len(s) {
		limit = len(s)
	}
	isASCII := true
	for i := 0; i < limit; i++ {
		if s[i] >= 0x80 {
			isASCII = false
			break
		}
	}
	if isASCII {
		return s[:n]
	}

	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

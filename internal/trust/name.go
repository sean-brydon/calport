package trust

import "strings"

// NameFromHostname turns a hostname into a peer label,
// e.g. "Alexs-MacBook-Pro.local" becomes "Alexs-MacBook-Pro".
func NameFromHostname(hostname, fallback string) string {
	name := strings.TrimSuffix(hostname, ".local")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name = strings.Trim(b.String(), "-._")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-._")
	}
	if !ValidName(name) {
		return fallback
	}
	return name
}

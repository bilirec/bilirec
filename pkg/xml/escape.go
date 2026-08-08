package xml

import "unicode/utf8"

// AppendEscapedSanitized appends s with invalid XML runes dropped and the
// five predefined entities escaped. Ranging over a string already replaces
// invalid UTF-8 with U+FFFD, which is valid in XML.
func AppendEscapedSanitized(buf []byte, s string) []byte {
	for _, r := range s {
		if invalidXMLRune(r) {
			continue
		}
		switch r {
		case '&':
			buf = append(buf, "&amp;"...)
		case '<':
			buf = append(buf, "&lt;"...)
		case '>':
			buf = append(buf, "&gt;"...)
		case '"':
			buf = append(buf, "&quot;"...)
		case '\'':
			buf = append(buf, "&#39;"...)
		default:
			buf = utf8.AppendRune(buf, r)
		}
	}
	return buf
}

func invalidXMLRune(r rune) bool {
	switch {
	case r < 0x20 && r != '\t' && r != '\n' && r != '\r':
		return true
	case r >= 0x7F && r <= 0x9F:
		return true
	case r == 0xFEFF || r == 0xFFFE || r == 0xFFFF:
		return true
	}
	return false
}

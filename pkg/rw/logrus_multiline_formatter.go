package rw

import (
	"strings"

	"github.com/sirupsen/logrus"
)

// MultiLineFormatter wraps standard TextFormatter but preserves literal newlines
type MultiLineFormatter struct {
	logrus.TextFormatter
}

// Format overrides the default log format logic
func (f *MultiLineFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	// Call standard formatter to get the base string
	b, err := f.TextFormatter.Format(entry)
	if err != nil {
		return nil, err
	}

	// Unescape the backslash-n sequences back to actual newlines.
	// Note: logrus will escape a literal "\\n" as "\\\\n"; preserve those.
	const placeholder = "\u0000"
	s := string(b)
	if strings.Contains(s, `\n`) {
		s = strings.ReplaceAll(s, `\\n`, placeholder)
		s = strings.ReplaceAll(s, `\n`, "\n")
		s = strings.ReplaceAll(s, placeholder, `\n`)
	}

	return []byte(s), nil
}

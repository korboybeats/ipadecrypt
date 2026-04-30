package device

import (
	"strings"
)

type Event struct {
	Name  string
	Attrs map[string]string
}

func (e Event) Attr(key string) string { return e.Attrs[key] }

// ParseEvent parses "@evt event=foo key=value ..." lines. Values may be
// double-quoted to allow spaces. Returns (_, false) for non-event lines.
func ParseEvent(line string) (Event, bool) {
	line = strings.TrimSpace(line)

	const prefix = "@evt "
	if !strings.HasPrefix(line, prefix) {
		return Event{}, false
	}

	rest := line[len(prefix):]
	attrs := map[string]string{}

	for _, tok := range splitTokens(rest) {
		eq := strings.IndexByte(tok, '=')
		if eq <= 0 {
			continue
		}

		k := tok[:eq]

		v := tok[eq+1:]
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}

		attrs[k] = v
	}

	return Event{Name: attrs["event"], Attrs: attrs}, true
}

func splitTokens(s string) []string {
	var (
		out []string
		cur strings.Builder
	)

	inQ := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQ = !inQ

			cur.WriteByte(c)
		case c == ' ' && !inQ:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}

	if cur.Len() > 0 {
		out = append(out, cur.String())
	}

	return out
}

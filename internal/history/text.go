package history

import (
	"strings"
	"unicode/utf8"
)

// Text converts terminal bytes to searchable text. It does not emulate a screen or
// parse messages. Original bytes remain authoritative.
type Text struct {
	state      byte
	pending    []byte
	previousCR bool
}

func (p *Text) Push(data []byte) string {
	text, _ := p.push(data, false)
	return text
}

// The optional mask identifies only the LF of an original CRLF pair.
func (p *Text) push(data []byte, display bool) (string, []bool) {
	data = append(p.pending, data...)
	p.pending = nil
	var out strings.Builder
	var hidden []bool
	for len(data) > 0 {
		if !utf8.FullRune(data) {
			p.pending = append([]byte(nil), data...)
			break
		}
		r, n := utf8.DecodeRune(data)
		data = data[n:]
		hide := p.previousCR && r == '\n'
		p.previousCR = r == '\r'
		switch p.state {
		case 'e':
			switch {
			case r == '[':
				p.state = 'c'
			case strings.ContainsRune("]P_^X", r):
				p.state = 's'
			case r >= ' ' && r <= '/':
				p.state = 'i'
			default:
				p.state = 0
			}
		case 'c':
			if r >= '@' && r <= '~' {
				p.state = 0
			}
		case 'i':
			if r >= '0' && r <= '~' {
				p.state = 0
			}
		case 's':
			if r == 7 {
				p.state = 0
			}
			if r == 27 {
				p.state = 't'
			}
		case 't':
			if r == '\\' {
				p.state = 0
			} else {
				p.state = 's'
			}
		default:
			switch {
			case r == 27:
				p.state = 'e'
			case r == '\r':
				out.WriteByte('\n')
				if display {
					hidden = append(hidden, false)
				}
			case r == '\n' || r == '\t' || r >= 32 && r != 127:
				out.WriteRune(r)
				if display {
					hidden = append(hidden, hide)
				}
			}
		}
	}
	return out.String(), hidden
}

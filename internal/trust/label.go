package trust

import (
	"fmt"
	"strings"
	"unicode"
)

// MaxLabelLen bounds a label, in characters: a name on a phone's screen.
const MaxLabelLen = 64

// CleanLabel is a label as the owner's screens show it: control and
// formatting characters (a terminal escape, a right-to-left override)
// removed, runs of spaces folded, trimmed, at most MaxLabelLen characters.
// A label is only ever shown: no trust decision reads it.
func CleanLabel(s string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		if n >= MaxLabelLen {
			break
		}
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar {
			continue
		}
		if space {
			if n+1 >= MaxLabelLen {
				break
			}
			b.WriteRune(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// RenameRelay gives a paired relay the owner's name for it ("PC du salon").
// Only the label changes: its keys, kind and addresses stay.
func (b *Bundle) RenameRelay(agentID, label string) error {
	label = CleanLabel(label)
	if label == "" {
		return fmt.Errorf("%w: a relay's name is empty", ErrMalformed)
	}
	for i := range b.Relays {
		if b.Relays[i].AgentID == agentID && agentID != "" {
			relays := append([]Relay(nil), b.Relays...)
			relays[i].Label = label
			b.Relays = relays
			return nil
		}
	}
	return fmt.Errorf("%w: no relay %q", ErrMalformed, agentID)
}

package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// MemberKind tells relatives from paying outsiders.
type MemberKind string

const (
	MemberRelative MemberKind = "proche"
	MemberExternal MemberKind = "externe"
)

// Member is a person who pays for the internet used by their devices.
type Member struct {
	ID        string     `json:"id"`
	RouterID  string     `json:"router_id"`
	Name      string     `json:"name"`
	Phone     string     `json:"phone"` // +221XXXXXXXXX, or "" if unknown
	Kind      MemberKind `json:"kind"`
	Active    bool       `json:"active"`    // inactive members are not invoiced
	Protected bool       `json:"protected"` // never invoiced nor cut (the owner)
	CreatedAt time.Time  `json:"created_at"`
	MACs      []MAC      `json:"macs"`
}

// Validate normalises and checks a member before it is stored.
func (m *Member) Validate() error {
	m.Name = strings.TrimSpace(m.Name)
	if n := utf8.RuneCountInString(m.Name); n < 2 || n > 80 {
		return fmt.Errorf("%w: nom requis (2 à 80 caractères)", ErrInvalidInput)
	}
	if strings.TrimSpace(m.Phone) != "" {
		p, err := NormalizeSenegalPhone(m.Phone)
		if err != nil {
			return err
		}
		m.Phone = p
	} else {
		m.Phone = ""
	}
	switch m.Kind {
	case MemberRelative, MemberExternal:
	case "":
		m.Kind = MemberRelative
	default:
		return fmt.Errorf("%w: type de membre inconnu %q", ErrInvalidInput, m.Kind)
	}
	return nil
}

// Billable reports whether monthly invoices apply to this member.
func (m Member) Billable() bool { return m.Active && !m.Protected }

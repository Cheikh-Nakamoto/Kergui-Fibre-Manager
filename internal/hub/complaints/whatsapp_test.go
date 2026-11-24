package complaints

import (
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Cmd
		ok   bool
	}{
		{"LISTE", Cmd{Verb: VerbList}, true},
		{"  liste ", Cmd{Verb: VerbList}, true},
		{"Liste", Cmd{Verb: VerbList}, true},
		{"VOIR 12", Cmd{Verb: VerbView, Number: 12}, true},
		{"voir #12", Cmd{Verb: VerbView, Number: 12}, true},
		{"Voir   # 7  ", Cmd{Verb: VerbView, Number: 7}, true},
		{"VOIR12", Cmd{Verb: VerbView, Number: 12}, true},
		{"R 12 Bonjour, c'est réglé.", Cmd{Verb: VerbReply, Number: 12, Text: "Bonjour, c'est réglé."}, true},
		{"r #3   Votre pass est crédité\nMerci", Cmd{Verb: VerbReply, Number: 3, Text: "Votre pass est crédité\nMerci"}, true},
		{"ok 4", Cmd{Verb: VerbClose, Number: 4}, true},
		{"OK #4", Cmd{Verb: VerbClose, Number: 4}, true},
		// Not commands: the help answers them.
		{"VOIR", Cmd{}, false},
		{"VOIR douze", Cmd{}, false},
		{"VOIR 12 maintenant", Cmd{}, false},
		{"R 12", Cmd{}, false},
		{"R bonjour", Cmd{}, false},
		{"OK", Cmd{}, false},
		{"LISTE 3", Cmd{}, false},
		{"Rien", Cmd{}, false},
		{"Bonjour", Cmd{}, false},
		{"", Cmd{}, false},
		{"12", Cmd{}, false},
		{"VOIR 99999999999999999999", Cmd{}, false},
	} {
		got, ok := ParseCommand(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("ParseCommand(%q) = %+v, %v; want %+v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestGuessCategory(t *testing.T) {
	for in, want := range map[string]string{
		"J'ai payé par Wave mais mon pass n'est pas actif": CategoryPayment,
		"Paiement non reçu":                         CategoryPayment,
		"Ma box ne marche plus depuis ce matin":     CategoryBox,
		"Pas d'internet, le wifi est très lent":     CategoryBox,
		"L'application plante au démarrage":         CategoryApp,
		"Bonjour, je voudrais parler à quelqu'un":   CategoryOther,
		"Mon appareil photo":                        CategoryOther,
		"Ma box Orange ne se connecte plus":         CategoryBox,
		"Je n'ai pas reçu mon remboursement svp !!": CategoryPayment,
	} {
		if got := GuessCategory(in); got != want {
			t.Errorf("GuessCategory(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestIsPleasantry(t *testing.T) {
	for in, want := range map[string]bool{
		"Merci":             true,
		"merci beaucoup !!": true,
		"Ok merci 🙏":        true,
		"👍":                 true,
		"Merci, mais ma box ne marche toujours pas": false,
		"Bonjour": false,
	} {
		if got := isPleasantry(in); got != want {
			t.Errorf("isPleasantry(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSummaryText(t *testing.T) {
	got := SummaryText(3, map[string]int{CategoryPayment: 2, CategoryBox: 1}, 1)
	want := "3 nouvelles plaintes (2 paiement, 1 box), dont 1 en attente depuis plus d'un jour. Répondez LISTE pour les voir."
	if got != want {
		t.Errorf("SummaryText = %q\nwant          %q", got, want)
	}
	got = SummaryText(1, map[string]int{CategoryOther: 1}, 0)
	if want := "1 nouvelle plainte (1 autre). Répondez LISTE pour la voir."; got != want {
		t.Errorf("SummaryText = %q, want %q", got, want)
	}
	if strings.Contains(got, "dont") {
		t.Errorf("no complaint waits, yet: %q", got)
	}
}

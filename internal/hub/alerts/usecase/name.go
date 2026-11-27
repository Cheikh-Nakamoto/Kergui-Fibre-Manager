package usecase

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// How a device is named to a household (audit M6): what it probably is, its
// maker when the name the box reported gives it away, and when it arrived
// — « Un téléphone Samsung, arrivé à 21 h 05 ». Never its MAC or its address
// on the network: neither means anything to the people who read the alert,
// and a name they cannot act on gets a family phone blocked or the alert
// ignored. The MAC stays in the alert for the audit trail.
//
// The hub has no maker table (the core's OUI table is empty, and phones
// mostly use private addresses anyway), so the guess reads the name the box
// reported. What it cannot read is « Un appareil ».

// dakar is the households' time zone. Senegal keeps UTC all year, with no
// summer time, so a fixed zone says the same as Africa/Dakar without the hub
// image having to ship the time zone database.
var dakar = time.FixedZone("Africa/Dakar", 0)

// kind is what a device probably is, as a French noun.
type kind struct {
	noun string
	fem  bool
}

var (
	kindDevice   = kind{"appareil", false}
	kindPhone    = kind{"téléphone", false}
	kindTablet   = kind{"tablette", true}
	kindComputer = kind{"ordinateur", false}
	kindTV       = kind{"télévision", true}
	kindPrinter  = kind{"imprimante", true}
	kindConsole  = kind{"console de jeux", true}
	kindWatch    = kind{"montre", true}
)

// kindWords name a kind outright; they win over a maker's usual kind, so a
// « Samsung-TV » is a television. The order matters: « androidtv » is a TV
// before it is an Android phone.
var kindWords = []struct {
	words []string
	kind  kind
}{
	{[]string{"tv", "smarttv", "androidtv", "appletv", "firetv", "webos", "lgwebostv", "bravia", "roku", "tizen"}, kindTV},
	{[]string{"playstation", "ps4", "ps5", "xbox", "nintendo"}, kindConsole},
	{[]string{"printer", "epson", "canon", "brother"}, kindPrinter},
	{[]string{"watch"}, kindWatch},
	{[]string{"tab", "tablet", "galaxytab", "ipad", "mediapad", "matepad"}, kindTablet},
	{[]string{"desktop", "laptop", "pc", "macbook", "imac", "mac", "thinkpad", "ideapad", "vivobook", "zenbook", "windows"}, kindComputer},
	{[]string{"phone", "iphone", "android", "mobile"}, kindPhone},
}

// makers are the brands a reported name gives away, with the kind they
// usually are. The phones sold in Senegal come first.
var makers = []struct {
	words []string
	name  string
	kind  kind
}{
	{[]string{"tecno"}, "Tecno", kindPhone},
	{[]string{"infinix"}, "Infinix", kindPhone},
	{[]string{"itel"}, "itel", kindPhone},
	{[]string{"samsung", "galaxy", "sm"}, "Samsung", kindPhone},
	{[]string{"iphone", "ipad", "macbook", "imac", "mac", "apple", "appletv"}, "Apple", kindDevice},
	{[]string{"xiaomi", "redmi", "poco", "mi"}, "Xiaomi", kindPhone},
	{[]string{"huawei", "mediapad", "matepad"}, "Huawei", kindPhone},
	{[]string{"honor"}, "Honor", kindPhone},
	{[]string{"oppo"}, "Oppo", kindPhone},
	{[]string{"realme"}, "Realme", kindPhone},
	{[]string{"oneplus"}, "OnePlus", kindPhone},
	{[]string{"vivobook", "zenbook", "asus"}, "Asus", kindComputer},
	{[]string{"vivo"}, "Vivo", kindPhone},
	{[]string{"nokia"}, "Nokia", kindPhone},
	{[]string{"motorola", "moto"}, "Motorola", kindPhone},
	{[]string{"pixel"}, "Google", kindPhone},
	{[]string{"lenovo", "thinkpad", "ideapad"}, "Lenovo", kindComputer},
	{[]string{"hp"}, "HP", kindComputer},
	{[]string{"dell"}, "Dell", kindComputer},
	{[]string{"acer"}, "Acer", kindComputer},
	{[]string{"lg", "lgwebostv", "webos"}, "LG", kindDevice},
	{[]string{"playstation", "ps4", "ps5", "sony", "bravia"}, "Sony", kindDevice},
	{[]string{"xbox"}, "Microsoft", kindConsole},
	{[]string{"nintendo"}, "Nintendo", kindConsole},
	{[]string{"epson"}, "Epson", kindPrinter},
	{[]string{"canon"}, "Canon", kindPrinter},
	{[]string{"brother"}, "Brother", kindPrinter},
	{[]string{"android"}, "Android", kindPhone},
}

// deviceName is a device's name, ready to be put in a sentence.
type deviceName struct {
	noun string // « téléphone Samsung », « iPhone », « appareil »
	fem  bool
	at   string // « 21 h 05 », or "" when the time is unknown
}

// nameOf guesses a device's name from what the box reported.
func nameOf(d domain.AlertDevice) deviceName {
	tokens := hostTokens(d.Hostname)
	k, maker, kindKnown := kindDevice, "", false
	for _, kw := range kindWords {
		if matchesAny(tokens, kw.words) {
			k, kindKnown = kw.kind, true
			break
		}
	}
	for _, m := range makers {
		if matchesAny(tokens, m.words) {
			maker = m.name
			if !kindKnown {
				k = m.kind
			}
			break
		}
	}
	n := deviceName{noun: k.noun, fem: k.fem}
	switch {
	case matchesAny(tokens, []string{"chromecast"}):
		n = deviceName{noun: "Chromecast"}
	case maker == "Apple" && k == kindPhone:
		n.noun = "iPhone"
	case maker == "Apple" && k == kindTablet:
		n = deviceName{noun: "iPad"}
	case maker != "":
		n.noun += " " + maker
	}
	if !d.AsOf.IsZero() {
		at := d.AsOf.In(dakar)
		n.at = fmt.Sprintf("%d h %02d", at.Hour(), at.Minute())
	}
	return n
}

// hostTokens splits a reported name into lower-case words: « TECNO-POP-10 »
// is tecno, pop, 10.
func hostTokens(host string) []string {
	return strings.FieldsFunc(strings.ToLower(host), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// matchesAny reports whether a word of the name is one of words. A short
// word must be the whole token (« mi », « hp », « tv »); a longer one may
// start it (« galaxys21 », « redminote9 », « epson3c2af1 »), and a long one
// may be anywhere in it (« awasiphone »).
func matchesAny(tokens, words []string) bool {
	for _, t := range tokens {
		for _, w := range words {
			switch {
			case t == w:
				return true
			case len(w) >= 4 && strings.HasPrefix(t, w):
				return true
			case len(w) >= 6 && strings.Contains(t, w):
				return true
			}
		}
	}
	return false
}

// e is the feminine agreement: « arrivée », « bloquée ».
func (n deviceName) e() string {
	if n.fem {
		return "e"
	}
	return ""
}

// pronoun is the object pronoun: « ne vous le signalera plus ».
func (n deviceName) pronoun() string {
	if n.fem {
		return "la"
	}
	return "le"
}

// indefinite is the name in a list: « Un téléphone Samsung, arrivé à 21 h 05 ».
func (n deviceName) indefinite() string {
	article := "Un "
	if n.fem {
		article = "Une "
	}
	s := article + n.noun
	if n.at != "" {
		s += ", arrivé" + n.e() + " à " + n.at
	}
	return s
}

// definite is the name as a sentence's subject: « Le téléphone Samsung
// arrivé à 21 h 05 ».
func (n deviceName) definite() string {
	var s string
	switch first, _ := firstRune(n.noun); {
	case strings.ContainsRune("aeiouyéèêàâîôûAEIOUYÉ", first):
		s = "L'" + n.noun
	case n.fem:
		s = "La " + n.noun
	default:
		s = "Le " + n.noun
	}
	if n.at != "" {
		s += " arrivé" + n.e() + " à " + n.at
	}
	return s
}

func firstRune(s string) (rune, bool) {
	for _, r := range s {
		return r, true
	}
	return 0, false
}

// DeviceLabel is how one device of an alert is named in a list:
// « Un téléphone Samsung, arrivé à 21 h 05 ».
func DeviceLabel(d domain.AlertDevice) string { return nameOf(d).indefinite() }

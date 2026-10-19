package domain

import "testing"

func TestParsePhone(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"+221 77 123 45 67", "+221771234567"},
		// The international prefix typed instead of a plus is the same number.
		{"00221771234567", "+221771234567"},
		{"+221-77-123-4567", "+221771234567"},
		{"(221) 77 123 4567", "+221771234567"},
	} {
		got, err := ParsePhone(tc.in)
		if err != nil {
			t.Errorf("ParsePhone(%q) = %v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("ParsePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "1234", "pas un numéro", "+221 77 123 45 67 89 01 23", "77a1234567", "0771234567"} {
		if _, err := ParsePhone(bad); err == nil {
			t.Errorf("ParsePhone(%q) was accepted", bad)
		}
	}
}

// TestPhoneMasked: a number must be recognisable by its owner and useless
// to anyone reading a log.
func TestPhoneMasked(t *testing.T) {
	if got := Phone("+221771234567").Masked(); got != "+221••••67" {
		t.Errorf("Masked() = %q", got)
	}
	if got := Phone("+123").Masked(); got != "••••" {
		t.Errorf("a short number leaks: %q", got)
	}
}

// TestReadIntent is the grammar of the replies this hub understands. It is
// forgiving about how people type and strict about what counts as a choice.
func TestReadIntent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		want    Keyword
		code    string
		choices []int
	}{
		{name: "the wa.me opt-in message", text: "KERGUI AB3D5K7M", want: KeywordOptIn, code: "AB3D5K7M"},
		{name: "opt-in, typed in lower case", text: "kergui ab3d5k7m", want: KeywordOptIn, code: "AB3D5K7M"},
		{name: "opt-in with no code", text: "Kergui", want: KeywordOptIn},
		{name: "stop", text: "stop", want: KeywordStop},
		{name: "stop in French", text: " Arret ", want: KeywordStop},
		{name: "one device", text: "1", want: KeywordChoice, choices: []int{1}},
		{name: "two devices", text: "1 2", want: KeywordChoice, choices: []int{1, 2}},
		{name: "the same device twice", text: "2 2", want: KeywordChoice, choices: []int{2}},
		{name: "a sentence with a number", text: "1er étage", want: KeywordNone},
		{name: "a question", text: "c'est quoi ?", want: KeywordNone},
		{name: "nothing", text: "   ", want: KeywordNone},
		{name: "zero is not a device", text: "0", want: KeywordNone},
		{name: "a number nobody would be shown", text: "100", want: KeywordNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReadIntent(tc.text)
			if got.Keyword != tc.want {
				t.Fatalf("ReadIntent(%q).Keyword = %v, want %v", tc.text, got.Keyword, tc.want)
			}
			if got.Code != tc.code {
				t.Errorf("Code = %q, want %q", got.Code, tc.code)
			}
			if len(got.Choices) != len(tc.choices) {
				t.Fatalf("Choices = %v, want %v", got.Choices, tc.choices)
			}
			for i, c := range tc.choices {
				if got.Choices[i] != c {
					t.Errorf("Choices = %v, want %v", got.Choices, tc.choices)
					break
				}
			}
		})
	}
}

// TestAlertDeviceLookup: a reply's number means the device the alert gave
// that number, and nothing else.
func TestAlertDeviceLookup(t *testing.T) {
	a := Alert{Devices: []AlertDevice{
		{No: 1, MAC: "aa:11:22:33:44:55"},
		{No: 2, MAC: "bb:22:33:44:55:66"},
	}}
	if d, ok := a.Device(2); !ok || d.MAC != "bb:22:33:44:55:66" {
		t.Errorf("Device(2) = %+v, %v", d, ok)
	}
	if _, ok := a.Device(3); ok {
		t.Error("Device(3) resolved to something: a reply must not reach a device the alert never showed")
	}
}

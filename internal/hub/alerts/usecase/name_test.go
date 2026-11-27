package usecase

import (
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// A device is named to a household by what it probably is, its maker and
// when it arrived (audit M6): never by its MAC or its address on the network.

var arrived = time.Date(2026, 11, 26, 21, 5, 0, 0, time.UTC)

func TestDeviceLabel_GuessesTheKindAndTheMakerFromTheName(t *testing.T) {
	for host, want := range map[string]string{
		"TECNO-POP-10":       "Un téléphone Tecno, arrivé à 21 h 05",
		"Galaxy-A12":         "Un téléphone Samsung, arrivé à 21 h 05",
		"SM-A125F":           "Un téléphone Samsung, arrivé à 21 h 05",
		"Samsung-TV":         "Une télévision Samsung, arrivée à 21 h 05",
		"Galaxy-Tab-A":       "Une tablette Samsung, arrivée à 21 h 05",
		"Awa-iPhone":         "Un iPhone, arrivé à 21 h 05",
		"iPad-de-Moussa":     "Un iPad, arrivé à 21 h 05",
		"MacBook-Pro":        "Un ordinateur Apple, arrivé à 21 h 05",
		"DESKTOP-4F2K9QX":    "Un ordinateur, arrivé à 21 h 05",
		"LAPTOP-HP-1234":     "Un ordinateur HP, arrivé à 21 h 05",
		"Redmi-Note-9":       "Un téléphone Xiaomi, arrivé à 21 h 05",
		"INFINIX-HOT-11":     "Un téléphone Infinix, arrivé à 21 h 05",
		"itel-A56":           "Un téléphone itel, arrivé à 21 h 05",
		"android-3f2a9c01d4": "Un téléphone Android, arrivé à 21 h 05",
		"Chromecast":         "Un Chromecast, arrivé à 21 h 05",
		"PS5-123":            "Une console de jeux Sony, arrivée à 21 h 05",
		"EPSON3C2AF1":        "Une imprimante Epson, arrivée à 21 h 05",
		"":                   "Un appareil, arrivé à 21 h 05",
		"ESP_1A2B3C":         "Un appareil, arrivé à 21 h 05",
	} {
		got := DeviceLabel(domain.AlertDevice{No: 1, MAC: "aa:11:22:33:44:55", Hostname: host, IP: "192.168.1.77", AsOf: arrived})
		if got != want {
			t.Errorf("%q: got %q, want %q", host, got, want)
		}
	}
}

func TestDeviceLabel_WithoutATimeSaysNoTime(t *testing.T) {
	if got := DeviceLabel(domain.AlertDevice{No: 1, MAC: "aa:11:22:33:44:55"}); got != "Un appareil" {
		t.Errorf("got %q", got)
	}
}

func TestDeviceLabel_TellsTheTimeInDakar(t *testing.T) {
	// Senegal keeps UTC all year: 09:05 UTC is 9 h 05 at home.
	d := domain.AlertDevice{No: 1, Hostname: "Galaxy-A12", AsOf: time.Date(2026, 11, 26, 9, 5, 0, 0, time.FixedZone("x", 3600))}
	if got := DeviceLabel(d); got != "Un téléphone Samsung, arrivé à 8 h 05" {
		t.Errorf("got %q", got)
	}
}

// twoDevices is an alert about a phone and a TV, the TV with no name at all.
func twoDevices() domain.Alert {
	return domain.Alert{ID: "a1", TenantID: tenantA, Devices: []domain.AlertDevice{
		{No: 1, MAC: "aa:11:22:33:44:55", Hostname: "TECNO-POP-10", IP: "192.168.1.77", AsOf: arrived},
		{No: 2, MAC: "aa:11:22:33:44:66", IP: "192.168.1.78", AsOf: arrived},
	}}
}

func oneDevice() domain.Alert {
	a := twoDevices()
	a.Devices = a.Devices[:1]
	return a
}

func TestComposeAlert_OneDeviceTeachesOneAndOK(t *testing.T) {
	text := ComposeAlert(oneDevice())
	if !strings.Contains(text, "1. Un téléphone Tecno, arrivé à 21 h 05") {
		t.Errorf("the device is not named: %q", text)
	}
	if !strings.HasSuffix(text, "Répondez 1 pour le bloquer, ou OK pour l'autoriser.") {
		t.Errorf("the reply rule is not « 1 blocks, OK authorises »: %q", text)
	}
	if strings.Contains(text, "2 pour") || strings.Contains(text, "ou 2") {
		t.Errorf("a one-device alert still teaches « 2 »: %q", text)
	}
}

func TestComposeAlert_SeveralDevicesTeachNumbersAndOK(t *testing.T) {
	text := ComposeAlert(twoDevices())
	for _, want := range []string{
		"1. Un téléphone Tecno, arrivé à 21 h 05",
		"2. Un appareil, arrivé à 21 h 05",
		"Répondez le numéro de l'appareil à bloquer (1, 2…), ou OK pour tous les autoriser.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
}

func TestComposePush_OpensToAuthoriseOrBlock(t *testing.T) {
	if got := ComposePush(oneDevice()); got != "Un téléphone Tecno, arrivé à 21 h 05, vient de rejoindre votre Wi-Fi. Ouvrez pour l'autoriser ou le bloquer." {
		t.Errorf("one device: %q", got)
	}
	if got := ComposePush(twoDevices()); got != "2 appareils viennent de rejoindre votre Wi-Fi : un téléphone Tecno, un appareil. Ouvrez pour les autoriser ou les bloquer." {
		t.Errorf("two devices: %q", got)
	}
}

func TestReplies_AgreeWithTheDevice(t *testing.T) {
	tv := domain.AlertDevice{No: 1, Hostname: "Samsung-TV", AsOf: arrived}
	phone := domain.AlertDevice{No: 1, Hostname: "Galaxy-A12", AsOf: arrived}
	for got, want := range map[string]string{
		ComposeBlocked(tv):                        "✅ La télévision Samsung arrivée à 21 h 05 est bloquée.",
		ComposeBlocked(phone):                     "✅ Le téléphone Samsung arrivé à 21 h 05 est bloqué.",
		ComposeAlreadyBlocked(phone):              "✅ Le téléphone Samsung arrivé à 21 h 05 était déjà bloqué.",
		ComposeRefused(tv, "raison"):              "❌ La télévision Samsung arrivée à 21 h 05 n'a pas été bloquée : raison",
		ComposeApproved(tv):                       "👍 La télévision Samsung arrivée à 21 h 05 est autorisée : Kergui ne vous la signalera plus.",
		ComposeApproved(phone):                    "👍 Le téléphone Samsung arrivé à 21 h 05 est autorisé : Kergui ne vous le signalera plus.",
		ComposeApproveFailed(phone):               "❌ Le téléphone Samsung arrivé à 21 h 05 n'a pas pu être autorisé. Ouvrez l'application Kergui pour réessayer.",
		ComposeBlockPending(phone):                "⏳ Le téléphone Samsung arrivé à 21 h 05 : demande envoyée, en attente de la box. Vous verrez le résultat dans l'application Kergui.",
		ComposeBlocked(domain.AlertDevice{No: 1}): "✅ L'appareil est bloqué.",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// TestHouseholdTexts_NameNoMACAndNoIP is the acceptance criterion: no text
// a household reads carries a MAC or an address on the network.
func TestHouseholdTexts_NameNoMACAndNoIP(t *testing.T) {
	alert := twoDevices()
	alert.Devices = append(alert.Devices, domain.AlertDevice{No: 3, MAC: "aa:11:22:33:44:77"})
	texts := []string{ComposeAlert(alert), ComposePush(alert), ComposePush(oneDevice()), PushTitle(alert)}
	for _, d := range alert.Devices {
		texts = append(texts, DeviceLabel(d), ComposeBlocked(d), ComposeAlreadyBlocked(d), ComposeRefused(d, "x"),
			ComposeApproved(d), ComposeApproveFailed(d), ComposeBlockPending(d))
	}
	for _, text := range texts {
		for _, leak := range []string{"aa:11", "AA:11", "192.168", "TECNO-POP"} {
			if strings.Contains(text, leak) {
				t.Errorf("%q shows %q", text, leak)
			}
		}
	}
}

// TestAlerts_TheMessageSaysWhenTheDeviceArrived: the time comes from the
// snapshot that saw the device, as the inventory's event carries it.
func TestAlerts_TheMessageSaysWhenTheDeviceArrived(t *testing.T) {
	repo, n, _, _, uc := bench()
	ev := firstSeen("w9", map[string]any{"no": 1, "mac": "aa:11:22:33:44:55", "device_id": "d1",
		"hostname": "Galaxy-A12", "as_of": arrived})
	if err := uc.HandleFirstSeen(t.Context(), ev); err != nil {
		t.Fatal(err)
	}
	if len(n.texts) != 1 || !strings.Contains(n.texts[0], "Un téléphone Samsung, arrivé à 21 h 05") {
		t.Fatalf("sent %q", n.texts)
	}
	// An event without a time names the device at the window's opening.
	ev = firstSeen("w10", map[string]any{"no": 1, "mac": "aa:11:22:33:44:56", "device_id": "d2"})
	if err := uc.HandleFirstSeen(t.Context(), ev); err != nil {
		t.Fatal(err)
	}
	if got := repo.alerts["w10"].Devices[0].AsOf; !got.Equal(t0) {
		t.Errorf("the device's time is %v, want the window's opening %v", got, t0)
	}
}

// TestHouseholdTexts_OneVocabulary (#118, audit H2): the push, its title and
// the WhatsApp alert speak the app's words, « à vérifier » and « autoriser »,
// never « approuver » nor an « inconnu ».
func TestHouseholdTexts_OneVocabulary(t *testing.T) {
	if got, want := PushTitle(oneDevice()), "Appareil à vérifier sur votre Wi-Fi"; got != want {
		t.Errorf("PushTitle = %q, want %q", got, want)
	}
	if got, want := PushTitle(twoDevices()), "2 appareils à vérifier sur votre Wi-Fi"; got != want {
		t.Errorf("PushTitle = %q, want %q", got, want)
	}
	if got := ComposeAlert(oneDevice()); !strings.HasPrefix(got, "Kergui : un appareil à vérifier vient de rejoindre votre Wi-Fi.") {
		t.Errorf("ComposeAlert = %q", got)
	}
	texts := []string{
		PushTitle(oneDevice()), PushTitle(twoDevices()), ComposeAlert(oneDevice()), ComposeAlert(twoDevices()),
		ComposePush(oneDevice()), ComposePush(twoDevices()), ComposeOptInConfirmation(""),
	}
	for _, d := range twoDevices().Devices {
		texts = append(texts, ComposeApproved(d), ComposeApproveFailed(d))
	}
	for _, text := range texts {
		low := strings.ToLower(text)
		for _, word := range []string{"approuv", "approb", "accept", "inconnu"} {
			if strings.Contains(low, word) {
				t.Errorf("%q says %q", text, word)
			}
		}
	}
}

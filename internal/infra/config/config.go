// Package config holds environment-variable names and default values for the CLI.
// It has no dependencies so any layer can reference the constants.
package config

import "time"

// Environment variable names.
const (
	// EnvMasterKey is the passphrase that encrypts stored router credentials.
	EnvMasterKey = "KERGUI_MASTER_KEY"
	// EnvRouterPassword supplies the router password without putting it in argv.
	EnvRouterPassword = "KERGUI_ROUTER_PASSWORD"
	// EnvNabooPayAPIKey enables payments; without it payment requests are refused.
	EnvNabooPayAPIKey = "KERGUI_NABOOPAY_API_KEY"
	// EnvNabooPayBaseURL overrides the NabooPay API URL (tests, sandbox).
	EnvNabooPayBaseURL = "KERGUI_NABOOPAY_BASE_URL"
	// EnvNabooPayWebhookSecret checks the signature of NabooPay's payment
	// webhooks (platform.naboopay.com, Settings → Integration). Empty = webhook off.
	EnvNabooPayWebhookSecret = "KERGUI_NABOOPAY_WEBHOOK_SECRET"
	// EnvPublicURL is where NabooPay sends the payer back after checkout.
	EnvPublicURL = "KERGUI_PUBLIC_URL"
)

// Defaults.
const (
	DefaultRouter    = "http://192.168.1.1"
	DefaultUsername  = "admin"
	DefaultDBPath    = "kergui.db"
	DefaultLogFile   = "kergui.log"
	DefaultPublicURL = "https://pay.cheikhmodiouf.org"
	DefaultTimeout   = 15 * time.Second
)

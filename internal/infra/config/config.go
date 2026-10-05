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
)

// Defaults.
const (
	DefaultRouter   = "http://192.168.1.1"
	DefaultUsername = "admin"
	DefaultDBPath   = "kergui.db"
	DefaultTimeout  = 15 * time.Second
)

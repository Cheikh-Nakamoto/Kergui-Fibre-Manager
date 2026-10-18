//go:build android || kergui_wasmsqlite

package sqlite

// On Android the app's seccomp filter kills processes that use legacy system
// calls (lstat, open, access...), which modernc.org/libc issues directly on
// linux/amd64. This driver runs the real SQLite compiled to WebAssembly
// (wazero) and does its I/O through Go's os package, which Android allows.
// The tag kergui_wasmsqlite selects it on other platforms, for tests.
import (
	_ "github.com/ncruces/go-sqlite3/driver" // registered as "sqlite3"
	_ "github.com/ncruces/go-sqlite3/embed"  // bundled sqlite3.wasm
)

const driverName = "sqlite3"

func dsn(path string) string {
	if path == ":memory:" {
		return path
	}
	return "file:" + path
}

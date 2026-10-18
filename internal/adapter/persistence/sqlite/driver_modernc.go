//go:build !android && !kergui_wasmsqlite

package sqlite

import _ "modernc.org/sqlite" // pure-Go SQLite, registered as "sqlite"

const driverName = "sqlite"

func dsn(path string) string { return path }

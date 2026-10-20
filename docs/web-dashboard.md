# Web dashboard

The web dashboard is implemented. Its source assets live in
[`internal/webui/assets`](../internal/webui/assets) (HTML/CSS/JS, no build step,
no dependencies) and are embedded with `go:embed`, so a single `kergui` binary
serves them.

Run it:

```sh
export KERGUI_MASTER_KEY='…'
kergui serve --addr 127.0.0.1:8080 --router http://192.168.1.1
# open http://127.0.0.1:8080
```

The dashboard talks only to the REST API (`internal/adapter/controller/httpapi`, documented in [api.md](api.md));
it holds no router logic of its own.

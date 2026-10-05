# web — dashboard

The web dashboard is implemented (Milestone 3). Its source assets live at
[`internal/webui/assets`](../internal/webui/assets) (HTML/CSS/JS, no build step)
and are embedded via `go:embed`, so a single `kergui` binary serves them.

Run it:

```sh
export KERGUI_MASTER_KEY='…'
kergui serve --addr 127.0.0.1:8080 --router http://192.168.1.1
# open http://127.0.0.1:8080
```

The dashboard talks only to the REST API (`internal/adapter/controller/httpapi`);
it holds no router logic of its own.

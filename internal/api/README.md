# internal/api — moved

The REST API now lives at
[`internal/adapter/controller/httpapi`](../adapter/controller/httpapi) (Milestone
2), beside the CLI controller — both are delivery adapters over the same use-case
interactors. Start it with `kergui serve`.

This directory is kept only as a signpost; the Milestone-3 web dashboard will live
under [`/web`](../../web).

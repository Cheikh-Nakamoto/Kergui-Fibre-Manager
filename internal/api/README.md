# internal/api — placeholder (Milestone 2)

The REST API (interface-adapter layer, `net/http`) lands in Milestone 2: HTTP
controllers + presenters over the same use-case interactors, exposing discovery,
device inventory, and (behind explicit confirmation) block/unblock.

It depends only inward on `internal/usecase` and its ports — never on a gateway
directly — and is wired in the composition root, exactly like the CLI controller.
Nothing here yet.

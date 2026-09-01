module nuxk.dev/horizon/core

go 1.24

// No third-party deps yet — stdlib only (net/http, log/slog, encoding/json).
// Keep it that way as long as possible: smaller binary, no supply-chain surface
// on a router. Add deps only with a clear reason.

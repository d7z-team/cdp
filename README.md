# CDP

[![Go Reference](https://pkg.go.dev/badge/gopkg.d7z.net/cdp.svg)](https://pkg.go.dev/gopkg.d7z.net/cdp)

CDP provides a native Go browser automation library and an embeddable MCP HTTP server for Chromium.

## Use as a Go library

Requires Go 1.26 or newer and a Chromium-based browser. Headless launch requires Chrome 142+ for native virtual screen support. Page runtime bundles are embedded, so library consumers do not need Node.js.

```sh
go get gopkg.d7z.net/cdp
```

The following fragment belongs inside a function returning `error`, with a caller-provided `ctx`:

```go
browser, err := cdp.Launch(ctx, cdp.LaunchOptions{})
if err != nil { return err }
defer browser.Close()

page, err := browser.NewPage(ctx)
if err != nil { return err }
if err := page.Navigate(ctx, "https://example.com", cdp.NavigateOptions{}); err != nil {
    return err
}
title, err := page.Title(ctx)
if err != nil { return err }
fmt.Println(title)
```

`Launch` starts and owns a browser; `Connect` attaches to an existing one. Operations accept a context and return errors. See the [API guide](docs/library-api.md) for ownership, cancellation and resource handling, or run the complete [basic example](examples/basic/main.go). The [connect example](examples/connect/main.go) shows how to attach to an existing browser.

| Public package | Purpose |
| --- | --- |
| `gopkg.d7z.net/cdp` | Browser, pages, locators, elements and CDP access |
| `gopkg.d7z.net/cdp/snapshot` | Snapshot documents, rendering, search and diff |
| `gopkg.d7z.net/cdp/mcpserver` | MCP HTTP handler using a caller-owned browser |

## Run an MCP server

From a repository checkout:

```sh
go run ./cmd/cdp-mcp
```

Connect an MCP client using Streamable HTTP at `http://127.0.0.1:3000/mcp`. The command manages a headless browser and provides a debug interface at `/debug`. It listens on loopback by default and has no built-in authentication.

See the [MCP guide](docs/mcp.md) for browser configuration, access control and HTTP embedding.

## Documentation

| Document | Read it for |
| --- | --- |
| [Go API guide](docs/library-api.md) | Resource ownership, contexts, data and errors |
| [MCP](docs/mcp.md) | CLI configuration, diagnostics and HTTP embedding |
| [Selectors](docs/selectors.md) | Layered fallback, exact elements and iframe queries |
| [Architecture](docs/architecture.md) | Package boundaries and runtime responsibilities |
| [Development](docs/development.md) | Setup, generation, builds and tests |
| [AGENTS.md](AGENTS.md) | Repository editing constraints |

Complete API signatures are available through `go doc` and the Go Reference link above. Detailed guides are written in Chinese.

## License

[MIT](LICENSE). Bundled third-party code retains its own license notices.

# MCP 使用指南

项目提供独立命令和可嵌入的 HTTP handler，两者均使用 Streamable HTTP。快速启动见 [README](../README.md)，Go 库的资源约定见 [Go API 指南](library-api.md)。

## 独立运行

在仓库根目录执行：

```sh
go run ./cmd/cdp-mcp
```

客户端连接 `http://127.0.0.1:3000/mcp`，浏览器调试界面位于 `/debug`。命令负责浏览器启动与退出清理，默认无头运行。

常用参数：

| 参数 | 用途 |
| --- | --- |
| `-no-headless` | 显示浏览器窗口 |
| `-browser-path` | 指定浏览器；默认自动查找 |
| `-user-data-dir` | 指定持久化 profile 目录 |
| `-host`、`-port` | 设置监听地址；默认 `127.0.0.1:3000` |
| `-diagnostics=runtime` | 开启 console 与未捕获异常采集 |

默认 profile 位于操作系统用户配置目录下的 `browser-mcp`，退出后保留。完整参数以 `go run ./cmd/cdp-mcp -help` 为准。

## 嵌入应用

通过 `mcpserver.New(browser, options)` 创建服务，将 `Handler()` 交给应用的 HTTP server。完整启动和关闭流程见 [HTTP 嵌入示例](../examples/mcp-http/main.go)。

MCP Server 借用 Browser，`Server.Close()` 只释放 MCP 资源。调用方分别管理 HTTP server 和 Browser 的关闭；调试界面通过 `EnableDebug` 显式开启。库代码与 MCP 共用浏览器时，应协调导航和快照操作，因为它们可能使已有元素引用失效。

## 诊断

诊断默认关闭，`browser_console` 此时返回 `diagnostics_disabled`。CLI 使用 `-diagnostics=runtime`；嵌入时在创建 Browser 的选项中设置 `Diagnostics: cdp.DiagnosticsRuntime`。

console 只提供订阅建立以来的有限事件，不代表完整页面历史。响应中的 `collection_started_at` 标明采集起点，`history_complete` 为 `false`。

## 访问控制

CLI 默认监听 loopback，不提供身份认证。对外部署时，由应用或反向代理提供访问认证。

MCP 与 Debug 均执行跨站请求校验。需要浏览器跨域访问 MCP 时，CLI 使用 `-cors-origin`，嵌入时设置 `AllowedOrigins`；该授权只作用于 MCP，不扩展到 Debug，也不替代身份认证。

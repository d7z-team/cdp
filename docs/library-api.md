# Go API 指南

模块路径为 `gopkg.d7z.net/cdp`。入门示例见 [README](../README.md)，完整签名与字段说明使用 `go doc gopkg.d7z.net/cdp` 查看；本指南侧重调用时需要理解的行为约定。

## 浏览器与资源所有权

`Launch` 拥有启动的浏览器进程。空 `UserDataDir` 使用临时 profile，关闭时清理；显式指定的目录保留。目录已被浏览器占用时返回 `ErrProfileInUse`。

`Connect` 借用已有浏览器。关闭连接会释放本连接的运行时资源，浏览器及其标签页继续运行。`Page.Close` 则显式关闭对应标签页。

| 资源 | 释放方式 |
| --- | --- |
| Browser | `Close()`，或用 `Shutdown(ctx)` 控制关闭预算 |
| 路由注册、事件订阅、下载/打印 watcher、Screencast | 使用结束后调用各自的 `Close()` |

下载和打印 watcher 应在触发动作前注册；取消一次等待不关闭 watcher。MCP 的嵌入与关闭约定见 [MCP 指南](mcp.md)。

## 浏览器环境

`LaunchOptions.WindowSize` 指定窗口外部尺寸，`LaunchOptions.Screen` 配置无头虚拟屏幕。两者宽高均使用 CSS 像素，`Screen.ScaleFactor` 指定设备像素比。无头屏幕默认至少为 1920×1080、缩放 1，并扩大到足以容纳显式窗口；显式屏幕小于窗口时返回配置错误。

```go
browser, err := cdp.Launch(ctx, cdp.LaunchOptions{
    WindowSize: cdp.WindowSize{Width: 1440, Height: 960},
    Screen: cdp.ScreenOptions{Width: 1920, Height: 1080, ScaleFactor: 2},
})
```

无头启动需要 Chrome 142+ 的原生虚拟屏幕支持。启动时校验屏幕配置，失败则返回错误。有头模式使用实际显示环境，`Screen` 保持零值；`Connect` 保留已有浏览器的显示配置。窗口与屏幕统一通过对应字段设置。

浏览器身份保持原生 UA 与 Client Hints；无头 UA 可能包含 `HeadlessChrome`。页面、iframe、Worker 的属性与请求头遵循浏览器自身行为。

## Context 与超时

I/O 方法以非 nil `context.Context` 为首参并返回错误。构造时的 context 只约束连接和初始化；成功后通过显式关闭管理资源生命周期。

`Timeouts` 属于 Browser 实例，使用 `time.Duration`。零值采用默认预算，负值无效；操作使用调用方 deadline 与配置预算中较早的期限。可配置项见 `go doc gopkg.d7z.net/cdp.Timeouts`。

## 定位与精确引用

`Locator` 是不可变查询计划，操作时查询目标；链式作用域、fallback、索引和 iframe 用法见 [选择器指南](selectors.md)。

`Locator.All(ctx)` 返回精确 `Element` 句柄。页面导航后句柄失效，不会自动改为操作新页面上的同名节点。`Page.Element(ctx, ref)` 解析当前快照引用；新快照也会使旧快照引用失效。遇到 `ErrStaleElement` 时，调用方应根据业务状态重新查询。

## 数据与扩展

- `Eval` 接收 JavaScript 函数体，例如 `return document.title`，并解码到 Go 目标；`EvalJSON` 返回 `json.RawMessage`。默认在页面 main world 执行。
- `Snapshot` 返回结构化文档；`snapshot` 包提供渲染、搜索和差异比较。页面与元素截图返回标准库 `*image.RGBA`，可直接使用 `image/png` 编码。
- `Fetch` 在浏览器环境执行，遵循页面 cookie 与 CORS 规则，取消操作会中止请求。
- 高级协议操作使用 `Session.Call/Subscribe`；需要明确执行环境时使用 `ExecutionContext`。初始化 binding、脚本和 lifecycle handler 通过构造选项的 `Initialize` 回调注册。

## 初始化与可观察性

`Navigate`、`Reload` 和历史导航返回后，当前文档的 binding 可用；网页最早脚本可能先于 binding 注册执行。依赖 binding 的自定义启动脚本需要等待其可用。用户 Eval 或输入一旦派发，不因上下文失效自动重放。

浏览器诊断与 Go 日志分别配置：

| 配置 | 行为 |
| --- | --- |
| `Diagnostics` | 默认 `DiagnosticsOff`；需要 console 与未捕获异常事件时，创建 `DiagnosticsRuntime` 实例。配置在连接生命周期内固定 |
| `Logger` | 接受 `*slog.Logger`，由 handler 决定级别与去向；默认不输出普通运行日志 |

关闭诊断时，订阅相应事件返回 `ErrDiagnosticsDisabled`，正常浏览器操作不受影响。底层 `Session.Call` 或其他 CDP 客户端仍可改变浏览器协议状态。

后台任务恢复 panic 时会报告错误与堆栈；没有实例 Logger 时使用 `slog.Default()`。MCP CLI 默认启用日志。

## 错误处理

使用 `errors.Is` 判断取消、关闭和失效等状态，用 `errors.As` 提取诊断。`OperationError` 标明操作，`LocatorError` 提供查询与匹配信息，`BrowserError` 保留浏览器侧原因。

```go
if err := page.ByTestID("submit").Click(ctx); err != nil {
    var detail *cdp.BrowserError
    if errors.As(err, &detail) {
        log.Printf("%s: %s", detail.Kind, detail.Detail)
    }
    return err
}
```

可编译示例见 [examples](../examples) 和 [example_test.go](../example_test.go)。

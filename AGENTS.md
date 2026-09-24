# AGENTS.md

本文件规定仓库修改约束。目录与依赖见 [架构说明](docs/architecture.md)，构建和测试命令见 [开发指南](docs/development.md)，调用行为见 [API 指南](docs/library-api.md)、[选择器指南](docs/selectors.md) 与 [MCP 指南](docs/mcp.md)。

## 公共 API 与代码组织

- 公开包为根 `cdp`、`snapshot`、`mcpserver`；公开签名使用公共类型。MCP 通过公开 API 操作浏览器。
- I/O API 以 context 为首参并返回 error；超时属于 Browser 实例。构造 context 仅约束建立过程。`Launch` 拥有进程，`Connect` 借用进程，MCP Server 借用 Browser。
- Locator 不可变；Element 保持精确节点语义。失效后交由调用方重新查询，派发副作用后不自动重试。
- Go 使用 gofmt，标准库、第三方与本模块导入分组。TypeScript 遵循仓库 strict 配置，协议 DTO 使用明确类型。
- 共享逻辑按职责集中；简单的单次转发直接内联，保留有独立职责的内部函数。清理函数前确认公共 API 边界。
- 图片使用标准库类型与编解码；调整截图时保留坐标投影、分块边缘、取消和清理语义。

## 错误契约

- 包装错误时保留 cause，支持 `errors.Is`、`errors.As` 和 `Unwrap`。浏览器错误 DTO 与 Go error 类型分离，通过统一入口构造。
- 所有公开浏览器操作写入明确 op，选择器、可操作性、frame bridge 和协议失败保留结构化原因。
- 错误 detail 限长，不携带完整 DOM、HTML 或大型候选数组。

## 页面运行时与生命周期

- `BrowserManager` 统一使用 `RegisterBinding`、`RegisterInitScript` 注册能力。初始化脚本逐个注入；清理脚本只调用 facade 的 `destroy()`。
- 同一 target 同时只进行一次绑定，事件推进 version，绑定期间的新 version 最多触发一个事件驱动 successor。已就绪集合只发布完成基础 domain、binding 与脚本初始化的 Page。
- 等待 Page 使用 `LoadPageContext`，保留调用 context 和原始初始化错误。Page 替换后，旧实例停止发布 lifecycle、binding 与 URL 状态。
- frame tree、导航和 target 事件驱动主动上下文初始化；Runtime 诊断事件不得成为初始化前提。binding 在当前文档显式安装后启动依赖脚本，runtime-ready 按 script、generation、context ID 去重。暂停的页面与 OOPIF 先完成预置注册，再恢复执行并确认运行时就绪；同步弹窗的 opener 不能等待暂停目标内的 JS 求值。
- 用户 `Page.Eval` 默认使用 page main。内部 FFI 运行于 isolated core；通过 namespace-aware context 和 `webassets.RuntimeOptionalMethodCall` / `RuntimeRequiredMethodCall` 调用。
- `CdpFFI` 是 isolated core 的统一 facade，内部模块不单独暴露到 window。main world 控制器通过 Go 生成的随机全局词法绑定访问，不挂载到 Window，也不向页面 hook 暴露控制器或绑定名。
- 内部 UI 通过 `internal/webassets/src/utils/internal_ui.ts` 的模块 registry 识别，截图隐藏与恢复复用该 registry。
- 交互复用 `runForegroundInteractionContext` 获取前台操作所有权，并使用同一个 context 激活页面和执行动作。稳定性诊断使用有界 `setTimeout` 采样，避免后台标签页的 rAF 节流。

## 查询与跨 frame 协议

- Go 只构造 layer 与 ordered fallback 计划、启动查询并消费 session/ref；TypeScript 统一解析和执行。每层第一个命中的 fallback 获胜，链式调用逐层推进。
- `Nth/First/Last` 使用 terminal filter；继续链式组合时可降为作用域过滤，保持先过滤再查询的顺序。
- XPath 统一使用 FontoXPath；调整其解析或执行时补充真实浏览器 E2E。
- iframe token `|iframe|` 由 Go/页面运行时生成。显式 frame 优先，隐式 fallback 仅作为 layer/path 执行期的单次补充。
- Frame bridge 的密钥只通过 CDP 注入 isolated world；消息使用认证加密，交付绑定当前文档的一次性凭据。校验响应的来源窗口、运行时、channel 与 request ID。
- 所有 iframe 纳入脚本注入；跨域通信复用 frame bridge。查询、坐标和 actionability 使用同一诊断链，每层文档负责本层元素或 frame 容器。
- `IsVisible` 保持可见性语义，`Click/Hover/Press` 使用结构化 actionability。跨域不可诊断与元素不可见分别报告。

## 生成代码

- Binding 从 `internal/engine/call_core.go` 的 struct 方法生成。方法首参为 `GoCallContext`，对外 JS 名由 `//gocall:name` 指定。
- Binding 名动态生成，Go/TS 必须一致；回调 ID 带 binding 名前缀，不假设固定名称。
- 修改生成源后重新生成 Go/TS binding；修改 `internal/webassets/src/` 后重建对应 `dest/` 产物。自动生成文件通过生成器维护。
- 页面构建入口为 `core`、`main_runtime`。具体生成命令和编译配置以源文件、Makefile、tsconfig 为准。

## HTTP 服务

- MCP 与 Debug 工具路由均执行跨站请求校验，Debug 的读取及 WebSocket 握手同样校验来源；MCP 的显式 CORS 授权不扩展到 Debug。
- HTTP 访问认证由嵌入方管理，CLI 默认监听 loopback。

## 测试与文档

- 单元测试覆盖纯逻辑、状态机、错误和资源管理。CDP 路由、target/session、binding 注入等协议行为使用真实浏览器 E2E，不搭建复杂 fake CDP 服务模拟。
- 浏览器测试检查 `browserEnabled`，未开启时 `t.Skip("set CDP_E2E_BROWSER=1")`。测试包通过 `TestMain` 独立管理 library 或 MCP 浏览器。
- Library 场景复用 `acquireSession(t)`，通过 `t.Cleanup(lease.Release)` 归还页面。同主题放在同一文件，每个场景直接编写为 `TestXxx`，避免 runXxx 转发。
- HTML fixture 放在 `e2e/fixture/pages/` 并通过 embed 加载，动态地址使用 `{{BASE_URL}}`。共享运行资源与单场景状态分离。
- 断言用户可观察结果、错误契约和生命周期；复用资源获取及导航流程，避免重复测试样板。
- 测试默认无头运行，浏览器路径为空时由库自动发现。沙箱限制导致缓存、端口或浏览器失败时，主动提权重跑后判断结果。
- 文档按读者用途组织，描述当前行为；API 细节集中在 Go 注释，可运行用法集中在 examples。

# 开发指南

目录与职责见 [架构说明](architecture.md)，代码修改约束见 [AGENTS.md](../AGENTS.md)。

## 环境准备

- Go 版本以 [go.mod](../go.mod) 为准。
- 修改页面运行时需要 Node.js 与 npm；可使用与 CI 一致的 Node.js 24，通过 `npm ci` 安装锁定依赖。
- 真实浏览器测试默认无头运行，需要 Chrome 142+；未指定路径时由库自动查找。

Go 库使用方无需安装前端构建工具。

## 修改与构建

| 修改范围 | 执行命令 |
| --- | --- |
| Go 代码 | `make fmt`；依赖变更时执行 `make tidy` |
| 页面 TypeScript | `make check-ts test-ts build-assets` |
| Binding 源 | `make generate`，然后执行页面 TypeScript 的检查与构建 |

Binding 源为 [call_core.go](../internal/engine/call_core.go)，生成入口由其 `go:generate` 声明维护。提交时包含对应的生成代码和 `internal/webassets/dest/` 产物。

`make inject` 完成 binding 生成、前端依赖安装、类型检查与脚本构建。所有命令以 [Makefile](../Makefile) 为准。

## 测试

| 命令 | 范围 |
| --- | --- |
| `make lint` | Go vet 与 TypeScript 静态检查 |
| `make test` | 全量 Go 测试，默认跳过真实浏览器场景 |
| `make test-race` | 全量 Go 测试与竞态检测 |
| `make test-ts` | 页面运行时初始化、失败重试与资源清理的逻辑测试 |
| `make test-e2e-browser` | library 与 MCP 的全部无头浏览器场景 |
| `make test-e2e-browser-headful` | 显示窗口运行全部浏览器场景 |

纯逻辑修改运行对应测试；页面脚本、协议、导航和交互修改同时运行真实浏览器场景。完整验收运行静态检查、Go 竞态测试、页面运行时逻辑测试和全部浏览器场景。

浏览器测试可能超过 300 秒。按主题调试可使用 Go 测试过滤：

```sh
CDP_E2E_BROWSER=1 go test -v -count=1 -timeout=15m -run 'TestScreenshot' ./e2e/scenarios/library
```

| 环境变量 | 用途 |
| --- | --- |
| `CDP_E2E_BROWSER=1` | 启用真实浏览器场景 |
| `CDP_E2E_HEADLESS=0` | 显示浏览器窗口 |
| `CDP_E2E_CHROME` | 指定浏览器路径 |
| `CDP_E2E_DIAGNOSTICS=runtime` | 为 library 套件启用 Runtime 诊断 |

Library 与 MCP 测试包独立管理浏览器。HTML 和 Worker 等资源分别放在 `e2e/fixture/pages/`、`e2e/fixture/assets/`，共享测试资源由 `e2e/harness/` 管理。页面运行时逻辑测试位于 `internal/webassets/tests/`；DOM 和 CDP 协议行为由浏览器测试覆盖。

## CI 与 fuzz

[Tests workflow](../.github/workflows/test.yml) 执行静态检查、竞态测试、页面运行时逻辑测试、生成产物同步检查，以及 library 的 off/runtime 模式和 MCP 浏览器测试。

[Fuzz workflow](../.github/workflows/fuzz.yml) 持续验证快照引用解析与分页游标，失败样本作为 artifact 保留。触发条件、运行预算和保留期限以 workflow 为准。

本地运行：

```sh
go test ./snapshot -run='^$' -fuzz='^FuzzParseRef$' -fuzztime=30s -parallel=2
go test ./snapshot -run='^$' -fuzz='^FuzzRenderCursor$' -fuzztime=30s -parallel=2
```

新增 fuzz 目标时同步更新 workflow 矩阵。将失败样本放入对应包的 `testdata/fuzz/`，普通 `go test` 即可执行回归。参考 [Go fuzz 文档](https://go.dev/doc/security/fuzz/)。

## 文档维护

README 负责入门与导航；API、选择器和 MCP 指南说明使用行为；架构说明维护职责与信任边界；本文件提供开发流程；AGENTS.md 维护修改约束。

行为变化同步更新对应指南与可编译示例。签名和字段细节以 Go 文档为准，配置清单以源码和命令帮助为准。生成的 API 文档通过生成流程维护，阶段性计划和实验日志保留在任务记录中。

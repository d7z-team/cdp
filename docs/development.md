# 开发指南

目录职责见 [架构说明](architecture.md)，修改约束见 [AGENTS.md](../AGENTS.md)。

## 环境准备

- Go 版本以 [go.mod](../go.mod) 为准，当前要求 Go 1.26。
- 修改页面运行时需要 Node.js 与 npm，Node.js 版本须满足构建依赖的 engines 要求。TypeScript 等构建依赖由 [package-lock.json](../package-lock.json) 锁定。
- 运行浏览器测试需要 Chromium 系浏览器。未指定可执行路径时由库自动查找。

在仓库根目录安装前端依赖：

```sh
npm ci
```

## 生成与构建

```sh
make generate     # 从 Go 源生成 Go/TypeScript binding
make check-ts     # TypeScript 类型与未使用代码检查
make build-assets # 构建嵌入 Go 的 JavaScript
```

Binding 源为 [call_core.go](../internal/engine/call_core.go)，生成命令由其 `go:generate` 声明维护。修改生成源后按上面的顺序执行；仅修改 TypeScript 时执行后两项。提交时包含对应的生成代码和 `internal/webassets/dest/` 产物。

`make inject` 合并执行 binding 生成、`npm ci`、类型检查和脚本构建。格式化 Go 使用 `make fmt`，依赖变更后使用 `make tidy`。

## 验证

| 命令 | 范围 |
| --- | --- |
| `make test` | 全部 Go 测试，默认跳过真实浏览器场景 |
| `make vet` | Go 静态检查 |
| `make check-ts` | TypeScript 静态检查 |
| `make test-race` | 全部 Go 包的竞态检测 |
| `make test-e2e` | E2E 包与 harness 测试，默认跳过真实浏览器场景 |
| `make test-e2e-browser` | 全部 library 与 MCP 浏览器场景，无头模式 |
| `make test-e2e-browser-headful` | 同上，显示浏览器窗口 |

浏览器测试可能超过 300 秒。按主题调试时直接使用 Go 的测试过滤：

```sh
CDP_E2E_BROWSER=1 go test -v -count=1 -run 'TestScreenshot' ./e2e/scenarios/library
```

`CDP_E2E_BROWSER=1` 启用浏览器场景；`CDP_E2E_HEADLESS=0` 显示窗口。`CDP_E2E_CHROME` 指定浏览器路径；`CDP_E2E_DIAGNOSTICS=runtime` 为 library 套件启用诊断模式。Library 与 MCP 测试包分别管理浏览器生命周期。页面 fixture 位于 `e2e/fixture/pages/`，共享运行资源由 `e2e/harness/` 管理。

根据修改范围选择检查：纯逻辑修改运行对应测试；页面脚本、协议、iframe、导航或交互变更同时运行相关真实浏览器场景。完整功能验收执行静态检查、Go 测试、竞态检测和全部浏览器场景。详细命令以 [Makefile](../Makefile) 为准。

## 持续集成与 fuzz

GitHub Actions 在 push、pull request 和手动触发时执行：

- [Tests](../.github/workflows/test.yml)：Go vet、全量竞态测试、TypeScript 检查，以及 binding 和脚本产物同步检查；真实 Chrome 分别运行 library 的 off/runtime 模式和 MCP 套件。
- [Fuzz](../.github/workflows/fuzz.yml)：快照引用解析与分页游标 fuzz，每个目标运行 30 秒；每周一 UTC 03:23 的定时任务延长到 5 分钟。失败样本作为 artifact 保留 14 天。

Go 版本读取 `go.mod`，前端构建使用 Node.js 24 和锁定的 npm 依赖。浏览器任务使用 Ubuntu runner 预装的 Chrome，并记录其版本。

本地运行单个 fuzz 目标：

```sh
go test ./snapshot -run='^$' -fuzz='^FuzzParseRef$' -fuzztime=30s -parallel=2
go test ./snapshot -run='^$' -fuzz='^FuzzRenderCursor$' -fuzztime=30s -parallel=2
```

新增 fuzz 目标时同步更新 workflow 的目标矩阵。失败样本还原到对应包的 `testdata/fuzz/` 后，普通 `go test` 会自动执行这些回归输入。编写约定见 [Go fuzz 文档](https://go.dev/doc/security/fuzz/)。

## 文档维护

README 提供入门与导航；Go API、选择器和 MCP 指南解释使用行为；架构说明维护职责和信任边界；本文件维护构建与验证流程；AGENTS.md 集中维护修改约束。方法签名和字段细节以 Go 文档为准，配置清单以配置文件和命令帮助为准。生成的 API 文档通过生成流程维护。

修改行为时更新对应指南与可编译示例。阶段性计划、测试日志和实验记录保留在任务记录中，仓库文档描述当前项目。

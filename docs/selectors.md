# 选择器指南

本指南说明查询语义与常见用法；完整方法签名使用 `go doc gopkg.d7z.net/cdp.Locator` 查看。

## 分层查询与 fallback

```go
button := page.Locator("form").Locator("#submit", "role=button[name='提交']")
if err := button.Click(ctx); err != nil { return err }
```

每次 `Locator` 增加一层。同一次调用的多个参数是该层的有序 fallback：第一个命中的选项获胜，不合并后续选项的结果。链式调用逐层推进。

单个选项中的 ` >> ` 分隔路径，可混用 CSS、XPath 和语义选择器：

```go
page.Locator("form >> button.submit")
page.Locator("#missing >> button", "#actual >> button")
```

## 选择语法

优先用 Go builder 表达语义查询，避免手工处理字符串转义：

```go
page.ByRole("button", cdp.RoleOptions{Name: "提交"})
page.ByText("Hello", cdp.TextOptions{Exact: true})
page.ByPlaceholder("搜索", cdp.TextOptions{})
page.ByLabel("用户名", cdp.TextOptions{})
page.ByTestID("submit")
```

需要组合路径时，可使用以下字符串语法：

| 类型 | 示例 |
| --- | --- |
| CSS | `#id`、`.card > span`、`css=.card` |
| XPath | `xpath=//button`、`//span[text()='确认']` |
| 角色与名称 | `role=button[name="提交"]` |
| 文本包含 / 精确文本 | `text="Hello"` / `text-is="Hello"` |
| 包含文本的祖先 | `has-text="Hello"` |
| 标签 / placeholder / test ID | `label="用户名"` / `placeholder="搜索"` / `testid=submit` |
| 相对兄弟 | `+button`、`~div` |

CSS 查询支持嵌套和动态创建的 open/closed author Shadow DOM；相对 XPath 可在指定 shadow 作用域执行，绝对 XPath 不隐式进入 Shadow DOM。XPath 遵循当前作用域，例如在 row 下查询 `//td[1]` 会限定到该 row。

## 索引与精确元素

```go
cards := page.Locator(".card")
thirdTitle := cards.Nth(2).Locator(".title")
first := cards.First()
last := cards.Last()
```

索引从零开始，派生操作不改变 `cards`。先选择第三张卡片再查询标题，与先查询全部标题再取第三项的作用域不同。

`All(ctx)` 返回当时匹配的精确 `Element`，适合逐节点读取或操作。Element 的子查询以该节点为根；根失效时返回错误。引用有效期见 [API 指南](library-api.md)。

## iframe

```go
frame := page.FrameLocator("#outer").FrameLocator("#inner")
if err := frame.ByTestID("submit").Click(ctx); err != nil { return err }
```

也可用 `Locator("#outer").ContentFrame()` 进入 iframe。已知 frame 时优先显式指定作用域；显式作用域优先于运行时的隐式 iframe fallback。同源与跨域 iframe 使用相同的定位 API。子 frame 暂不可用时，定位操作在超时预算内等待；无法访问 frame 与元素不可见会分别报告。

## 等待与交互

```go
status := page.ByTestID("status")
if err := status.WaitForText(ctx, "ready", cdp.TextOptions{Exact: true}); err != nil {
    return err
}
if err := page.ByTestID("submit").Click(ctx); err != nil { return err }
```

`IsVisible` 和可见状态等待检查 DOM/CSS 可见性，不会因为元素位于视口外就判定不可见，也不隐式滚动。`Click/Hover/Press` 使用可操作性诊断，包含遮挡与 frame 链检查；准备阶段可以等待和滚动，派发副作用后不自动重试。

等待受调用 context 和 Browser 的超时预算约束，详见 [Go API 指南](library-api.md)。

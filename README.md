# http

基于标准库 `net/http` 的 HTTP
客户端封装，面向业务调用的便捷请求库，开箱即用、零第三方依赖。仓库地址：<https://github.com/laocc/go-http>。

## 特性

- `Get` / `Post` 便捷入口 + 函数式 `Option` 逐请求配置，方法可任意扩展
- 请求超时（默认 10s）与 context 取消，逐请求单独指定
- URL Query 合并、单条/批量请求头、`Host` 头支持
- 请求体：JSON 结构体 / 原始字符串 / `[]byte` / `io.Reader` / 表单（`url.Values`、`map[string]string`）
- `Host` 强制指定连接 IP（等价 cURL `CURLOPT_RESOLVE`），仅改拨号地址，连接池仍可复用
- 状态码判正常 / 异常：默认仅 200 正常，`Allow` 可追加 201 / 204 等
- 响应体自动读取并关闭连接；JSON / XML 一键反序列化（按响应头自动推断）
- 可选日志回调（`Debug`），请求摘要与耗时接入你自己的日志体系
- `IsTimeout` 统一判断超时错误

## 安装

```bash
go get github.com/laocc/go-http
```

## 快速开始

```go
import (
"github.com/laocc/go-http"
)

// GET，附带查询参数
response, requestErr := http.Get("https://api.example.com/ping", http.WithQuery(map[string]string{
"key": "value",
}))
if requestErr != nil {
if http.IsTimeout(requestErr) {
// 超时，可做重试或降级
}
return requestErr
}

// POST JSON，超时 5 秒
response, requestErr = http.Post("https://api.example.com/order",
http.WithJSON(map[string]any{"goods": "apple", "count": 2}),
http.Header("X-Token", "your-token"),
http.Timeout(5*time.Second),
)
if requestErr != nil {
return requestErr
}
if response.IsWrong { // 状态码不在允许列表内（默认仅 200）即为异常
return fmt.Errorf("接口异常: %d %s", response.StatusCode, response.Html)
}

// 反序列化响应体
var result struct {
Code int    `json:"code"`
Msg  string `json:"msg"`
}
_ = response.Json(&result)
```

## 常用 API

### 请求入口

| 函数                   | 说明      |
|----------------------|---------|
| `Get(url, opts...)`  | GET 请求  |
| `Post(url, opts...)` | POST 请求 |

### 请求配置（Option）

| 配置                                            | 说明                                               |
|-----------------------------------------------|--------------------------------------------------|
| `WithQuery(map[string]string)`                | 追加 URL 查询参数                                      |
| `Header(name, value)` / `Headers(map)`        | 设置请求头                                            |
| `WithBody(string / []byte / io.Reader / any)` | 原始请求体；`any` 按 JSON 编码                            |
| `WithJSON(any)`                               | 结构体 JSON 编码，自动带 `Content-Type: application/json` |
| `WithForm(map[string]string / url.Values)`    | 表单体，自动带对应 Content-Type                           |
| `ContentType(string)`                         | 显式指定 Content-Type                                |
| `Timeout(duration)`                           | 请求超时（默认 `DefaultTimeout`）                        |
| `WithContext(ctx)`                            | 自定义 context（取消 / 链路传递）                           |
| `Allow(codes...)`                             | 追加视为正常的响应状态码（如 201、204）                          |
| `Decode(DecodeJSON / DecodeXML)`              | 指定响应体解析方式（默认按响应头推断）                              |
| `Host(ip, port...)`                           | 强制连接指定 IP（如域名被劫持 / 内网映射场景）                       |
| `WithClient(*http.Client)`                    | 临时替换底层客户端                                        |
| `Debug(LogFunc)`                              | 注入日志回调，记录请求摘要与耗时                                 |

### 响应（`*Response`）

| 字段 / 方法                 | 说明                         |
|-------------------------|----------------------------|
| `StatusCode` / `Status` | 状态码与状态文本                   |
| `IsWrong`               | 状态码是否异常（不在允许列表内）           |
| `Body` / `Html`         | 响应体原文（`[]byte` / `string`） |
| `Json(target)`          | 按推断方式（JSON / XML）反序列化到结构体  |
| `Xml(target)`           | 强制按 XML 反序列化               |
| `DebugInfo()`           | 整个响应的分行 JSON 文本，可直接落日志   |
| `Header`                | 响应头                        |
| `RemoteIP`              | 目标服务器实际 IP（httptrace 捕获）   |
| `Used`                  | 请求耗时                       |

## 更多用法

完整可运行示例见 [demo/demo.go](./demo/demo.go)，每种调用方式独立一个 func。

### 表单提交

```go
response, requestErr := http.Post("https://api.example.com/login",
http.WithForm(map[string]string{"user": "tom", "pass": "123456"}),
)
```

### XML 接口

```go
response, _ := http.Post("https://api.example.com/soap",
http.WithBody(xmlBody),
http.Decode(http.DecodeXML), // 也可不传，按响应头 Content-Type 自动推断
)
var result struct {
Code string `xml:"code"`
}
_ = response.Json(&result)
```

### 强制连接指定 IP（等价 CURLOPT_RESOLVE）

```go
// URL 与 Host 头、TLS SNI 仍是原域名，只是实际连接指向 1.2.3.4
response, requestErr := http.Get("https://api.example.com/ping", http.Host("1.2.3.4"))
```

### 把请求日志接到自己的日志体系

```go
http.Post(url, http.WithBody(body), http.Debug(func (title string, args ...any) {
log.Printf("[http] %s: %v\n", title, args)
}))
```

### 追加正常状态码

```go
response, _ := http.Post("https://api.example.com/job", http.WithBody(body), http.Allow(204))
if response.IsWrong { // 200 与 204 都算正常
return fmt.Errorf("状态异常: %d", response.StatusCode)
}
```

## License

[MIT](./LICENSE)

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
- `IsTimeout` 统一判断超时错误
- `DebugInfo()` 输出整个响应的分行 JSON，可直接落日志

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
response := http.Get("https://api.example.com/ping", http.WithQuery(http.Map(
"key", "value",
)))
if response.Error != nil {
if http.IsTimeout(response.Error) {
// 超时，可做重试或降级
}
return response.Error
}

// POST JSON，超时 5 秒
response = http.Post("https://api.example.com/order",
http.WithJSON(map[string]any{"goods": "apple", "count": 2}),
http.Header("X-Token", "your-token"),
http.Timeout(5*time.Second),
)
if response.Error != nil {
return response.Error
}
if response.IsWrong { // 状态码不在允许列表内（默认仅 200）即为异常
return fmt.Errorf("接口异常: %d %s", response.StatusCode, response.Html())
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
| `WithQuery(map[string]interface{})`           | 追加 URL 查询参数（配合 `Map` 使用）                         |
| `Header(name, value)` / `Headers(map[string]interface{})` | 设置请求头（批量配合 `Map` 使用）                |
| `UserAgent(string)`                           | 指定 User-Agent（默认见 `http.go` 的 `userAgent` 常量）   |
| `WithBody(string / []byte / io.Reader / any)` | 原始请求体；`any` 按 JSON 编码                            |
| `WithJSON(any)`                               | 结构体 JSON 编码，自动带 `Content-Type: application/json` |
| `WithForm(map[string]interface{} / url.Values)` | 表单体，自动带对应 Content-Type                         |
| `ContentType(string)`                         | 显式指定 Content-Type                                |
| `Timeout(duration)`                           | 请求超时（默认 `DefaultTimeout`）                        |
| `WithContext(ctx)`                            | 自定义 context（取消 / 链路传递）                           |
| `Allow(codes...)`                             | 追加视为正常的响应状态码（如 201、204）                          |
| `Decode(DecodeJSON / DecodeXML)`              | 指定响应体解析方式（默认按响应头推断）                              |
| `Host(ip, port...)`                           | 强制连接指定 IP（如域名被劫持 / 内网映射场景）                       |
| `WithClient(*Client)`                         | 临时替换客户端（用 `NewClient()` 链式配置，见「客户端构建器」） |

### 响应（`*Response`）

| 字段 / 方法                 | 说明                         |
|-------------------------|----------------------------|
| `Error`                 | 请求过程中的错误（超时、连接失败等），成功时为 `nil`    |
| `StatusCode` / `Status` | 状态码与状态文本                   |
| `IsWrong`               | 状态码是否异常（不在允许列表内）           |
| `Method` / `Url`        | 请求方式与最终请求的 URL             |
| `ReqHeaders`            | 实际发送出去的请求头                  |
| `ResHeaders`            | 收到的响应头                      |
| `Data`                  | 发送的原文（POST 等带请求体的请求才有）      |
| `Body` / `Html()`       | 响应体原文（`[]byte` / `string`） |
| `Json(target)`          | 按推断方式（JSON / XML）反序列化到结构体  |
| `Xml(target)`           | 强制按 XML 反序列化               |
| `RemoteIP`              | 目标服务器实际 IP（httptrace 捕获）   |
| `RemotePort`            | 目标服务器端口（httptrace 捕获）       |
| `Start`                 | 请求开始时间（Unix 毫秒）             |
| `Used`                  | 请求耗时                       |
| `DebugInfo()`           | 整个响应的分行 JSON 文本，可直接落日志     |

## 更多用法

完整可运行示例见 [demo/demo.go](./demo/demo.go)，每种调用方式独立一个 func。

### 表单提交

```go
response := http.Post("https://api.example.com/login",
http.WithForm(http.Map("user", "tom", "pass", "123456")),
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
response := http.Get("https://api.example.com/ping", http.Host("1.2.3.4"))
```

### 追加正常状态码

```go
response, _ := http.Post("https://api.example.com/job", http.WithBody(body), http.Allow(204))
if response.IsWrong { // 200 与 204 都算正常
return fmt.Errorf("状态异常: %d", response.StatusCode)
}
```

### Map 快速构造参数

`WithQuery` / `Headers` / `WithForm` 都接受 `map[string]interface{}`，值可以是任意类型（内部转字符串）。用 `Map` 按「键、值」成对构造最省事：

```go
http.Map("page", 1, "size", 20) // map[page:1 size:20]

response := http.Get(url, http.WithQuery(http.Map("page", 1, "size", 20)))
response = http.Post(url, http.WithForm(http.Map("user", "tom", "pass", "123456")))
```

### 客户端构建器（NewClient）

底层 `http.Client` 交给构建器描述，**发请求时才真正创建**（`Build` 结果会被缓存，同一份配置全局只创建一次，连接池因此得以复用）：

```go
client := http.NewClient().
MaxIdleConns(200).
MaxIdleConnsPerHost(50). // 标准库默认仅 2，并发场景务必调大
IdleConnTimeout(90*time.Second).
DialTimeout(10*time.Second).
TLSHandshakeTimeout(10*time.Second).
HTTP2(true)

response := http.Get(url, http.WithClient(client))

// 或设为全局默认，之后所有请求生效
http.DefaultClient = http.NewClient().MaxIdleConnsPerHost(50)
```

支持的配置项：

| 分类 | 方法 |
| --- | --- |
| 代理 | `Proxy(url)` / `ProxyEnv()` / `ProxyFunc(fn)` / `ProxyConnectHeader(header)` |
| 建连 | `DialTimeout(d)` / `KeepAlive(d)` |
| TLS | `TLSConfig(*tls.Config)` / `MinTLSVersion(v)` / `InsecureSkipVerify()` / `RootCA(file)` / `ClientCert(cert, key)` / `TLSHandshakeTimeout(d)` |
| 连接池 | `MaxIdleConns(n)` / `MaxIdleConnsPerHost(n)` / `MaxConnsPerHost(n)` / `IdleConnTimeout(d)` / `DisableKeepAlives(bool)` |
| 超时与限制 | `ResponseHeaderTimeout(d)` / `ExpectContinueTimeout(d)` / `MaxResponseHeaderBytes(n)` / `Timeout(d)` |
| 协议与缓冲 | `HTTP2(bool)` / `DisableCompression(bool)` / `WriteBufferSize(n)` / `ReadBufferSize(n)` |
| 其他 | `CookieJar(jar)` / `UseCookieJar()` / `Redirect(fn)` / `NoRedirect()` / `From(*http.Client)` |

配置过程中的错误（代理地址格式错误、证书读取失败等）不会中断请求，可用 `client.Err()` 查询。

### 链式构建器（NewHttp）

先 `NewHttp` 创建构建器，链式配置后发起请求，配置会保留可复用：

```go
response := http.NewHttp().
Timeout(5*time.Second).
Allow(204).
Header("X-Token", "token").
Get("https://api.example.com/order")
```

分步配置并多次复用：

```go
builder := http.NewHttp()
builder.Timeout(3*time.Second).Header("X-Token", "token")

response := builder.Get("https://api.example.com/order")
response = builder.Post("https://api.example.com/order", http.WithJSON(params))

builder.Reset() // 换一套配置前清空
```

链式可用的方法与包级 Option 一一对应：`Timeout` / `Allow` / `Header` / `Headers` / `UserAgent` / `ContentType` / `WithQuery` / `WithBody` / `WithJSON` / `WithForm` / `Decode` / `Host` / `WithClient` / `WithContext`，另有 `Reset` 清空、`Do(method, url)` 自定义方法。

## License

[MIT](./LICENSE)

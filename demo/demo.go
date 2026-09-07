package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	req "github.com/laocc/go-http"
)

// demoURL 示例接口地址，可替换成自己的接口
const demoURL = "https://httpbin.org"

func main() {

	// 每个示例都是独立的 func，按需取消注释运行：
	_ = GetWithQuery()
	_ = GetWithHeaders()
	_ = PostJSON()
	_ = PostForm()
	_ = PostRawBody()
	_ = PostWithContentType()
	_ = GetWithTimeout()
	_ = GetWithContext()
	_ = PostWithAllowStatus()
	_ = GetDecodeXML()
	_ = GetWithHostIP()
	_ = PostWithCustomClient()
	_ = ClientFullOptions()
	_ = ClientWithConnPool()
	_ = ClientWithProxy()
	_ = ClientWithTLS()
	_ = ClientWithMutualTLS()
	_ = ClientWithTransportPolicy()
	_ = ClientAsGlobalDefault()
	_ = ClientDefault()
	_ = ClientTuned()
	_ = ClientLongWait()
	_ = ClientBuildOnce()
	_ = BuildChain()
	fmt.Println("ok")
}

// GetWithQuery GET 请求 + Query 参数（会合并到 URL 已有的 QueryString 上）
func GetWithQuery() error {
	response := req.Get(demoURL+"/get", req.WithQuery(req.Map(
		"page", "1",
		"size", "20",
	)))
	if response.Error != nil {
		return response.Error
	}
	if response.IsWrong { // 状态码不在允许列表内（默认仅 200）
		return fmt.Errorf("请求失败: %d %s", response.StatusCode, response.Html())
	}
	fmt.Println("状态码:", response.StatusCode, "耗时:", response.Used)
	fmt.Println("响应原文:", response.Html())
	return nil
}

// GetWithHeaders GET 请求 + 自定义请求头（单条 Header 与批量 Headers 可混用）
func GetWithHeaders() error {
	response := req.Get(demoURL+"/headers",
		req.Header("X-Token", "your-token"),
		req.Headers(req.Map(
			"X-Request-Id", "abc-123",
			"Accept", "application/json",
		)),
	)
	if response.Error != nil {
		return response.Error
	}
	if response.IsWrong {
		return fmt.Errorf("请求失败: %d", response.StatusCode)
	}
	var result struct {
		Headers map[string]string `json:"headers"`
	}
	if jsonErr := response.Json(&result); jsonErr != nil {
		return jsonErr
	}
	fmt.Printf("服务端收到的请求头: %+v\n", result.Headers)
	return nil
}

// PostJSON POST 提交 JSON：结构体（或 map）自动编码，并带上 Content-Type: application/json
func PostJSON() error {
	type orderParams struct {
		Goods string `json:"goods"`
		Count int    `json:"count"`
	}
	response := req.Post(demoURL+"/post",
		req.WithJSON(orderParams{Goods: "apple", Count: 2}),
		req.Timeout(5*time.Second),
	)
	if response.Error != nil {
		return response.Error
	}
	if response.IsWrong {
		return fmt.Errorf("状态码异常: %d %s", response.StatusCode, response.Html())
	}
	var result struct {
		JSON orderParams `json:"json"`
	}
	if jsonErr := response.Json(&result); jsonErr != nil {
		return jsonErr
	}
	fmt.Printf("服务端回显: %+v\n", result.JSON)
	return nil
}

// PostForm POST 提交表单：支持 Map 构造的 map[string]interface{} 与 url.Values（同名参数用 Add）
func PostForm() error {
	// 方式一：Map 构造，值可以是任意类型（内部转字符串）
	response := req.Post(demoURL+"/post", req.WithForm(req.Map(
		"user", "tom",
		"pass", "123456",
	)))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("表单提交状态码:", response.StatusCode)

	// 方式二：url.Values，可提交同名参数（如多选框、批量标签）
	formValues := url.Values{}
	formValues.Add("tag", "go")
	formValues.Add("tag", "http")
	response = req.Post(demoURL+"/post", req.WithForm(formValues))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("多值表单提交状态码:", response.StatusCode)
	return nil
}

// PostRawBody POST 提交原始请求体：string / []byte / io.Reader 三种形态
func PostRawBody() error {
	// 1. 字符串（未指定 Content-Type 时按 DefaultContentType，即 JSON 发送）
	response := req.Post(demoURL+"/post", req.WithBody(`{"raw":"string"}`))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("string 请求体:", response.StatusCode)

	// 2. 字节切片，适合已序列化好的报文（如签名后的原文）
	response = req.Post(demoURL+"/post", req.WithBody([]byte(`{"raw":"bytes"}`)))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("[]byte 请求体:", response.StatusCode)

	// 3. io.Reader，适合大报文或流式读取（内部会读完再发送）
	response = req.Post(demoURL+"/post",
		req.WithBody(strings.NewReader(`{"raw":"reader"}`)),
		req.ContentType("application/json;charset=UTF-8"),
	)
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("io.Reader 请求体:", response.StatusCode)
	return nil
}

// PostWithContentType 显式指定 Content-Type（部分第三方接口要求非 JSON 类型）
func PostWithContentType() error {
	response := req.Post(demoURL+"/post",
		req.WithBody("name=tom&age=18"),
		req.ContentType("text/plain;charset=UTF-8"),
	)
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("自定义 Content-Type 提交:", response.StatusCode)
	return nil
}

// GetWithTimeout 超时控制：默认 DefaultTimeout 10 秒，可用 Timeout 逐请求覆盖；IsTimeout 判断超时错误
func GetWithTimeout() error {
	// 该接口 3 秒后才响应，这里只给 2 秒，必然超时
	response := req.Get(demoURL+"/delay/3", req.Timeout(2*time.Second))
	if response.Error != nil {
		if req.IsTimeout(response.Error) {
			return fmt.Errorf("请求超时: %v", response.Error)
		}
		return response.Error
	}
	fmt.Println("状态码:", response.StatusCode)
	return nil
}

// GetWithContext 传入自定义 context：可主动取消请求，或与链路追踪上下文打通
func GetWithContext() error {
	requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 业务中可在任意时机调用 cancel() 主动中断请求
	response := req.Get(demoURL+"/get", req.WithContext(requestCtx))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("状态码:", response.StatusCode)
	return nil
}

// PostWithAllowStatus 追加正常状态码：默认只认 200，用 Allow 追加 201/204/202 等
func PostWithAllowStatus() error {
	// 该接口返回 204：不传 Allow 时 response.IsWrong 为 true
	response := req.Post(demoURL+"/status/204", req.Allow(204))
	if response.Error != nil {
		return response.Error
	}
	if response.IsWrong { // 200 与 204 都算正常，其余码才为 true
		return fmt.Errorf("状态异常: %d %s", response.StatusCode, response.Html())
	}
	fmt.Println("204 被视为正常，IsWrong =", response.IsWrong)
	return nil
}

// GetDecodeXML 处理 XML 响应：Decode 显式指定解析方式，之后用 Json 即按 XML 解析
// （不指定时也支持按响应头 Content-Type 自动推断）
func GetDecodeXML() error {
	response := req.Get(demoURL+"/xml", req.Decode(req.DecodeXML))
	if response.Error != nil {
		return response.Error
	}
	var result struct {
		Title string `xml:"title"`
	}
	// 方法名沿用 Json，实际解析方式由 Response.Decode 决定；也可直接用 Xml 强制按 XML 解析
	if decodeErr := response.Json(&result); decodeErr != nil {
		return decodeErr
	}
	fmt.Println("XML 标题:", result.Title)
	return nil
}

// GetWithHostIP 强制连接指定 IP（等价 cURL CURLOPT_RESOLVE）：
// URL、Host 请求头与 TLS SNI 仍是原域名，证书校验不受影响；port 省略时沿用 URL 端口
func GetWithHostIP() error {
	response := req.Get("https://api.example.com/ping",
		req.Host("1.2.3.4"), // 也可指定端口：req.Host("1.2.3.4", 8443)
	)
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("实际连接 IP:", response.RemoteIP, "耗时:", response.Used)
	return nil
}

// PostWithCustomClient 临时替换底层客户端（连接池、代理、TLS 等自定义配置）
func PostWithCustomClient() error {
	client := req.NewClient().
		MaxIdleConns(100).
		MaxIdleConnsPerHost(10).
		IdleConnTimeout(90 * time.Second)

	response := req.Post(demoURL+"/post",
		req.WithJSON(map[string]any{"from": "custom-client"}),
		req.WithClient(client),
	)
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("自定义客户端请求:", response.StatusCode)
	return nil
}

// ClientWithConnPool 连接池配置：一个 client 复用长连接，避免每次请求重新 TCP 握手与 TLS 握手。
//
// 两个前提：
//  1. Transport 必须复用——它是连接池的载体，每次请求 new 一个等于白白丢掉长连接，
//     还会因 TIME_WAIT 堆积耗尽本地端口；通常全局建一个，或按接口方各建一个；
//  2. MaxIdleConnsPerHost 默认值只有 2，高并发场景务必调大，否则多余连接会被立刻关闭重建。
//
// 完整字段清单见 ClientFullOptions。
func ClientWithConnPool() error {
	client := req.NewClient().
		DialTimeout(10 * time.Second). // 建立 TCP 连接的超时
		KeepAlive(30 * time.Second). // TCP 保活探测间隔
		TLSHandshakeTimeout(10 * time.Second).
		ExpectContinueTimeout(1 * time.Second).
		MaxIdleConns(200). // 连接池总上限
		MaxIdleConnsPerHost(50). // 单个域名的空闲连接上限（标准库默认仅 2）
		IdleConnTimeout(90 * time.Second).
		HTTP2(true) // 显式开启 HTTP/2
	// 这里不设 Timeout：整体超时统一交给 req.Timeout 选项控制，
	// 否则 client.Timeout 会与本库的 context 超时叠加（谁先到谁先生效）

	const concurrentTotal = 20
	var waiter sync.WaitGroup
	statusList := make(chan int, concurrentTotal)
	for index := 0; index < concurrentTotal; index++ {
		waiter.Add(1)
		go func() {
			defer waiter.Done()
			response := req.Get(demoURL+"/get",
				req.WithClient(client),
				req.Timeout(5*time.Second),
			)
			if response.Error != nil {
				statusList <- 0
				return
			}
			statusList <- response.StatusCode
		}()
	}
	waiter.Wait()
	close(statusList)

	successTotal := 0
	for statusCode := range statusList {
		if statusCode == http.StatusOK {
			successTotal++
		}
	}
	fmt.Printf("并发 %d 次，成功 %d 次（全程复用同一连接池）\n", concurrentTotal, successTotal)
	return nil
}

// ClientWithProxy 代理配置：固定代理地址，或跟随环境变量（HTTP_PROXY / HTTPS_PROXY / NO_PROXY）
func ClientWithProxy() error {
	// 方式一：固定代理地址（地址格式错误时不会中断，可用 Err 查询）
	fixedClient := req.NewClient().Proxy("http://127.0.0.1:7890")
	if fixedClient.Err() != nil {
		return fixedClient.Err()
	}
	response := req.Get(demoURL+"/get", req.WithClient(fixedClient))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("固定代理请求:", response.StatusCode)

	// 方式二：跟随环境变量，部署环境切换代理时无需改代码
	envClient := req.NewClient().ProxyEnv()
	response = req.Get(demoURL+"/get", req.WithClient(envClient))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("环境变量代理请求:", response.StatusCode)
	return nil
}

// ClientWithTLS TLS 配置：收紧最低版本、跳过证书校验、信任自签 CA
func ClientWithTLS() error {
	// 场景一：收紧最低 TLS 版本（生产推荐）
	strictClient := req.NewClient().MinTLSVersion(tls.VersionTLS12)
	response := req.Get(demoURL+"/get", req.WithClient(strictClient))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("最低 TLS1.2 请求:", response.StatusCode)

	// 场景二：跳过证书校验（仅测试环境，存在中间人风险，生产禁用）
	insecureClient := req.NewClient().InsecureSkipVerify()
	response = req.Get("https://self-signed.example.com/api", req.WithClient(insecureClient))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("跳过证书校验请求:", response.StatusCode)

	// 场景三：信任指定 CA（内网证书、自签证书的正确做法，比跳过校验安全）
	// 证书读取/解析失败不会中断，可用 Err 查询
	caClient := req.NewClient().RootCA("./certs/ca.pem")
	if caClient.Err() != nil {
		return caClient.Err()
	}
	response = req.Get("https://inner.example.com/api", req.WithClient(caClient))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("自签 CA 请求:", response.StatusCode)
	return nil
}

// proxyAuthHeader 构造代理鉴权头（HTTPS 走 CONNECT 时附加）
func proxyAuthHeader() http.Header {
	header := http.Header{}
	header.Set("Proxy-Authorization", "Basic base64(user:pass)")
	return header
}

// ClientWithMutualTLS 双向 TLS：接口方要求客户端出示证书（金融、政务类接口常见）
func ClientWithMutualTLS() error {
	client := req.NewClient().ClientCert("./certs/client.pem", "./certs/client.key")
	if client.Err() != nil {
		return client.Err()
	}
	response := req.Get("https://api.example.com/secure",
		req.WithClient(client),
		req.Timeout(10*time.Second),
	)
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("双向 TLS 请求:", response.StatusCode)
	return nil
}

// ClientWithTransportPolicy 细粒度传输策略：分阶段超时、重定向策略、Cookie 管理
func ClientWithTransportPolicy() error {
	client := req.NewClient().
		DialTimeout(5 * time.Second).
		TLSHandshakeTimeout(5 * time.Second). // TLS 握手超时
		ResponseHeaderTimeout(10 * time.Second). // 发出请求到收到响应头（不含读取响应体）
		IdleConnTimeout(60 * time.Second).
		UseCookieJar(). // 自动维护 Cookie，适合需要登录态的接口
		NoRedirect() // 不跟随重定向，直接拿到 30x
	if client.Err() != nil {
		return client.Err()
	}
	response := req.Get(demoURL+"/redirect/1", req.WithClient(client))
	if response.Error != nil {
		return response.Error
	}
	// 注意：本库默认只认 200 正常，30x 会置 IsWrong = true，需自行判断或用 Allow 追加
	fmt.Println("未跟随重定向，状态码:", response.StatusCode)
	if response.StatusCode == http.StatusFound {
		fmt.Println("跳转地址:", response.ResHeaders.Get("Location"))
	}
	return nil
}

// ClientAsGlobalDefault 全局替换默认客户端：建议在程序启动时设置一次，
// 之后所有不传 WithClient 的请求都会走它（否则用的是包内 DefaultClient）
func ClientAsGlobalDefault() error {
	req.DefaultClient = req.NewClient().
		MaxIdleConnsPerHost(50).
		IdleConnTimeout(90 * time.Second).
		MinTLSVersion(tls.VersionTLS12)

	response := req.Get(demoURL + "/get") // 无需再传 WithClient
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("走全局默认客户端:", response.StatusCode)
	return nil
}

// ClientFullOptions 客户端全参数示例：按用途分组列出 NewClient 支持的配置项，逐项标注含义与建议值。
// 复制后按自己的场景删减即可；未配置的项会使用库内置默认值。
//
// 几条通用原则：
//  1. 构建器只描述配置，真正的 http.Client 在发请求前才 Build，且结果会被缓存——
//     同一份配置全局只创建一次底层客户端，连接池因此得以复用；
//  2. 超时建议统一用本库的 req.Timeout（context 控制），不要再设 Timeout，
//     否则两个超时叠加、谁短谁先生效，排查问题会很迷惑；
//  3. 本库已把响应体读完并关闭 Body，连接会正常归还池子，长连接能真正复用；
//  4. 配合本库 Host 选项时，Transport 会被克隆并缓存（不会每次重建），连接池依然有效。
func ClientFullOptions() error {
	client := req.NewClient().
		// ===== 代理 =====
		Proxy("http://127.0.0.1:7890"). // 固定代理；ProxyEnv 跟随环境变量；ProxyFunc 自定义
		ProxyConnectHeader(proxyAuthHeader()). // 仅对 HTTPS 的 CONNECT 请求生效（HTTP 明文请求不带）
		// ===== 建连 =====
		DialTimeout(10 * time.Second). // 建立 TCP 连接的超时（DNS 之后）
		KeepAlive(30 * time.Second). // 保活探测间隔
		// ===== TLS（证书校验、最低版本、客户端证书等）=====
		TLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}). // 生产环境建议不低于 1.2
		TLSHandshakeTimeout(10 * time.Second).
		// ===== 连接池 =====
		MaxIdleConns(500). // 全局空闲连接总数上限
		MaxIdleConnsPerHost(100). // 单个域名的空闲连接上限（标准库默认仅 2）
		MaxConnsPerHost(0). // 单域名连接总数上限（含正在使用），0 不限；给对端限流时用
		IdleConnTimeout(90 * time.Second).
		DisableKeepAlives(false). // true = 每次请求新建连接（短连接），不建议
		// ===== 超时与限制 =====
		ResponseHeaderTimeout(10 * time.Second). // 发完请求到收到响应头的等待上限（不含读取响应体）
		ExpectContinueTimeout(1 * time.Second).
		MaxResponseHeaderBytes(1 << 20). // 响应头最大字节数（此处 1MB）
		// ===== 协议与缓冲区 =====
		HTTP2(true). // 显式启用 HTTP/2
		DisableCompression(false). // true = 不自动解压 gzip（自行处理，省一次解压开销）
		WriteBufferSize(32 * 1024).
		ReadBufferSize(32 * 1024)
	if client.Err() != nil {
		return client.Err()
	}

	response := req.Get(demoURL+"/get",
		req.WithClient(client),
		req.Timeout(5*time.Second),
	)
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("全参数客户端请求:", response.StatusCode, "对端 IP:", response.RemoteIP)
	return nil
}

// BuildChain 链式构建器用法：NewHttp 创建后链式配置，最后 Get / Post 发起请求。
// 构建器上的配置会一直保留，可多次复用；Reset 清空，Get / Post 临时传入的 Option 可覆盖同名配置。
func BuildChain() error {
	// 一次写完
	response := req.NewHttp().
		Timeout(5*time.Second).
		Allow(204).
		Header("X-Token", "your-token").
		UserAgent("MyClient/1.0").
		WithQuery(req.Map("page", 1)). // 值可以是任意类型，内部转字符串
		Get(demoURL + "/get")
	if response.Error != nil {
		return response.Error
	}
	if response.IsWrong {
		return fmt.Errorf("链式 GET 失败: %d", response.StatusCode)
	}
	fmt.Println("链式 GET:", response.StatusCode)

	// 分步配置后复用同一套配置
	builder := req.NewHttp()
	builder.Timeout(3 * time.Second).Headers(req.Map("X-Token", "your-token"))

	response = builder.Get(demoURL + "/get")
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("复用构建器 GET:", response.StatusCode)

	// POST 时临时追加请求体（单次 Option 排在构建器配置之后，可覆盖同名配置）
	response = builder.Post(demoURL+"/post", req.WithJSON(map[string]any{"goods": "apple"}))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("复用构建器 POST:", response.StatusCode)

	// 换一套配置前清空
	builder.Reset()
	response = builder.Get(demoURL + "/get")
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("Reset 后请求:", response.StatusCode)
	return nil
}

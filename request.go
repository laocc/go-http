package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// WithClient 指定本次请求使用的底层 http.Client（默认 DefaultClient）
func WithClient(client *http.Client) Option {
	return func(cfg *options) {
		if client != nil {
			cfg.client = client
		}
	}
}

// WithContext 指定本次请求的 context（用于取消/传递链路上下文）
func WithContext(ctx context.Context) Option {
	return func(cfg *options) {
		if ctx != nil {
			cfg.ctx = ctx
		}
	}
}

// WithQuery 追加 URL 查询参数（会合并到 URL 已有的 QueryString 中）
func WithQuery(params map[string]string) Option {
	return func(cfg *options) {
		if cfg.query == nil {
			cfg.query = make(map[string]string, len(params))
		}
		for key, value := range params {
			cfg.query[key] = value
		}
	}
}

// Timeout 指定本次请求超时；不指定时使用 DefaultTimeout
func Timeout(requestTimeout time.Duration) Option {
	return func(cfg *options) {
		if requestTimeout > 0 {
			cfg.timeout = requestTimeout
		}
	}
}

// resolveClientCache 定向 IP 的客户端缓存（key 为「底层客户端指针|ip:port」）。
// 避免每次请求都克隆一个 Transport，导致连接池无法复用、空闲连接堆积。
var resolveClientCache sync.Map

// Debug 指定本次请求的日志输出方式（默认不记录）。
// 注入回调后，每次请求完成或失败都会触发回调：title 为日志标题，
// args 为记录内容（方法/URL/请求头/请求体/状态码/响应内容/耗时/错误等），
// 便于把请求日志接到自己的日志体系：
//
//	http.Post(url, http.WithBody(body), http.Debug(func(title string, args ...any) {
//		log.Println(title, args)
//	}))
func Debug(logFunc LogFunc) Option {
	return func(cfg *options) {
		cfg.logCall = logFunc
	}
}

// Host 强制本次请求连到指定 IP（等价 PHP cURL 的 CURLOPT_RESOLVE：
// $cOption[CURLOPT_RESOLVE] = ["域名:端口:IP"]）。
// 无论 URL 里的域名解析到哪，实际连接都指向 ip；URL、Host 请求头与 HTTPS 的 TLS SNI 仍是原域名，
// 因此证书校验仍按域名进行，不会报证书不匹配。
//
// port 可省略：不传（或传 0）时按请求的 URL 端口连，
// 即 URL 未写端口时走协议默认值（http 80 / https 443），URL 写了端口（如 :8080）则沿用该端口。
//
//	http.Get("https://api.example.com/ping", http.Host("1.2.3.4"))        // 连 1.2.3.4:443
//	http.Get("https://api.example.com/ping", http.Host("1.2.3.4", 8443))  // 连 1.2.3.4:8443
func Host(ip string, port ...int64) Option {
	return func(cfg *options) {
		if ip == "" {
			return
		}
		cfg.host = ip
		cfg.port = 0
		if len(port) > 0 {
			cfg.port = port[0]
		}
	}
}

// resolveClient 克隆出一个把连接目标固定到 ip:port 的客户端：只改拨号地址，其余配置沿用 base。
// base 的 Transport 不是 *http.Transport（自定义 RoundTripper）时无法安全改写拨号逻辑，退回 base。
func resolveClient(base *http.Client, ip string, port int64) *http.Client {
	transport, isHTTPTransport := base.Transport.(*http.Transport)
	if base.Transport == nil {
		transport, isHTTPTransport = http.DefaultTransport.(*http.Transport)
	}
	if !isHTTPTransport || transport == nil {
		return base
	}

	target := net.JoinHostPort(ip, strconv.FormatInt(port, 10))
	cacheKey := fmt.Sprintf("%p|%s", base, target)
	if cached, exists := resolveClientCache.Load(cacheKey); exists {
		if cachedClient, isClient := cached.(*http.Client); isClient {
			return cachedClient
		}
	}

	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	clonedTransport := transport.Clone()
	clonedTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialTarget := target
		if port <= 0 {
			// 未指定端口时沿用请求的 URL 端口。
			// 传输层传入的 address 已按协议补全端口（http:80 / https:443），
			// URL 显式写了端口（如 :8080）时则是该端口。
			if _, originPort, splitErr := net.SplitHostPort(address); splitErr == nil && originPort != "" {
				dialTarget = net.JoinHostPort(ip, originPort)
			}
		}
		return dialer.DialContext(ctx, network, dialTarget)
	}

	clonedClient := *base
	clonedClient.Transport = clonedTransport
	resolveClientCache.Store(cacheKey, &clonedClient)
	return &clonedClient
}

// Allow 在默认允许的状态码（见 http.go 的 allowCode，默认 200）之外追加允许项，支持一次传多个。
// 少量接口用 204、201 等表示成功，传了之后 Response.IsWrong 会把它们判为正常：
//
//	response, _ := http.Post(url, http.WithBody(body), http.Allow(204))
//	if response.IsWrong { // 200 与 204 都算正常，其余码视为异常
//		return fmt.Errorf("状态异常: %d", response.StatusCode)
//	}
func Allow(codes ...int64) Option {
	return func(cfg *options) {
		if cfg.allowCodes == nil {
			cfg.allowCodes = make(map[int]bool, len(codes))
		}
		for _, code := range codes {
			if code > 0 {
				cfg.allowCodes[int(code)] = true
			}
		}
	}
}

// Header 追加单个请求头；重复键以后调用者覆盖先前的值
func Header(name, value string) Option {
	return func(cfg *options) {
		if cfg.headers == nil {
			cfg.headers = make(map[string]string)
		}
		cfg.headers[name] = value
	}
}

// Headers 批量设置请求头（与 Header 合并，重复键由调用顺序决定）
func Headers(headers map[string]string) Option {
	return func(cfg *options) {
		if cfg.headers == nil {
			cfg.headers = make(map[string]string, len(headers))
		}
		for name, value := range headers {
			cfg.headers[name] = value
		}
	}
}

// ContentType 显式指定 Content-Type（优先级最高）
func ContentType(contentType string) Option {
	return func(cfg *options) {
		cfg.contentType = contentType
	}
}

// Decode 指定响应体的解析方式（DecodeJSON / DecodeXML），默认按响应头 Content-Type 推断，推断不出时按 JSON。
// 少数接口返回 XML：
//
//	response, _ := http.Post(url, http.WithBody(body), http.Decode(http.DecodeXML))
//	var result SomeXmlStruct // 需带 `xml:"..."` 标签
//	response.Json(&result)   // 此处实际按 XML 解析
func Decode(decode string) Option {
	return func(cfg *options) {
		if decode != "" {
			cfg.decode = decode
		}
	}
}

// WithBody 设置原始请求体，支持 string / []byte / io.Reader；
// 其它类型会尝试 JSON 编码并按 JSON 内容处理。
// 请求体非空时若未显式指定 Content-Type，会按 DefaultContentType（JSON）发送；
// 需要显式指定时用 ContentType，结构体直接 JSON 编码推荐用 WithJSON。
func WithBody(body any) Option {
	return func(cfg *options) {
		switch value := body.(type) {
		case nil:
			return
		case string:
			cfg.body = []byte(value)
		case []byte:
			cfg.body = value
		case io.Reader:
			readerBody, readErr := io.ReadAll(value)
			if readErr != nil {
				cfg.encodeErr = fmt.Errorf("读取请求体失败: %v", readErr)
				return
			}
			cfg.body = readerBody
		default:
			jsonBody, encodeErr := json.Marshal(body)
			if encodeErr != nil {
				cfg.encodeErr = fmt.Errorf("请求体 JSON 编码失败: %v", encodeErr)
				return
			}
			cfg.body = jsonBody
			cfg.contentType = DefaultContentType
		}
	}
}

// WithJSON 把任意结构 JSON 编码为请求体，并自动设置 Content-Type: application/json
func WithJSON(body any) Option {
	return func(cfg *options) {
		if body == nil {
			return
		}
		jsonBody, encodeErr := json.Marshal(body)
		if encodeErr != nil {
			cfg.encodeErr = fmt.Errorf("请求体 JSON 编码失败: %v", encodeErr)
			return
		}
		cfg.body = jsonBody
		cfg.contentType = DefaultContentType
	}
}

// WithForm 把 map[string]string / url.Values 编码为表单体，
// 并自动设置 Content-Type: application/x-www-form-urlencoded
func WithForm(form any) Option {
	return func(cfg *options) {
		formValues := url.Values{}
		switch value := form.(type) {
		case url.Values:
			formValues = value
		case map[string]string:
			for key, item := range value {
				formValues.Set(key, item)
			}
		case map[string][]string:
			for key, items := range value {
				for _, item := range items {
					formValues.Add(key, item)
				}
			}
		case nil:
			return
		default:
			cfg.encodeErr = fmt.Errorf("表单请求体只支持 map[string]string / url.Values，收到 %T", form)
			return
		}
		cfg.body = []byte(formValues.Encode())
		cfg.contentType = "application/x-www-form-urlencoded;charset=UTF-8"
	}
}

// Get 发起 GET 请求（通用出口的便捷封装）
func Get(rawURL string, optionList ...Option) (*Response, error) {
	return doRequest(http.MethodGet, rawURL, optionList...)
}

// Post 发起 POST 请求（通用出口的便捷封装）
func Post(rawURL string, optionList ...Option) (*Response, error) {
	return doRequest(http.MethodPost, rawURL, optionList...)
}

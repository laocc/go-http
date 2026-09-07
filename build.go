package http

import (
	"context"
	"net/http"
	"time"
)

// Http 链式请求构建器：先 NewHttp 创建，再用链式方法逐项配置，最后用 Get / Post 发起请求。
//
// 典型用法（一次写完）：
//
//	response := http.NewHttp().
//		Timeout(5*time.Second).
//		Allow(204).
//		Header("X-Token", "token").
//		Get("https://api.example.com/order")
//
// 也可以分步配置，再反复复用同一套配置：
//
//	builder := http.NewHttp()
//	builder.Timeout(5 * time.Second).Header("X-Token", "token")
//
//	response := builder.Get("https://api.example.com/order")
//	response = builder.Post("https://api.example.com/order", http.WithJSON(params))
//
//	builder.Reset() // 换一套配置前清空
//
// 配置会一直累积在构建器上，之后每次请求都会带上；Get / Post 额外传入的 Option 排在后面，
// 因此可以对单次请求覆盖构建器上的同名配置（如临时换个超时时间）。
type Http struct {
	optionList []Option
}

// NewHttp 创建一个链式请求构建器
func NewHttp() *Http {
	return &Http{}
}

// Timeout 设置请求超时（默认 DefaultTimeout）
func (builder *Http) Timeout(requestTimeout time.Duration) *Http {
	builder.optionList = append(builder.optionList, Timeout(requestTimeout))
	return builder
}

// Allow 追加视为正常的响应状态码（如 201、204）
func (builder *Http) Allow(codes ...int64) *Http {
	builder.optionList = append(builder.optionList, Allow(codes...))
	return builder
}

// Header 设置单个请求头
func (builder *Http) Header(name string, value string) *Http {
	builder.optionList = append(builder.optionList, Header(name, value))
	return builder
}

// Headers 批量设置请求头（推荐配合 Map 使用）
func (builder *Http) Headers(headers map[string]interface{}) *Http {
	builder.optionList = append(builder.optionList, Headers(headers))
	return builder
}

// UserAgent 指定 User-Agent（默认见 http.go 的 userAgent 常量）
func (builder *Http) UserAgent(value string) *Http {
	builder.optionList = append(builder.optionList, UserAgent(value))
	return builder
}

// ContentType 显式指定 Content-Type
func (builder *Http) ContentType(contentType string) *Http {
	builder.optionList = append(builder.optionList, ContentType(contentType))
	return builder
}

// WithQuery 追加 URL 查询参数（推荐配合 Map 使用）
func (builder *Http) WithQuery(params map[string]interface{}) *Http {
	builder.optionList = append(builder.optionList, WithQuery(params))
	return builder
}

// WithBody 设置原始请求体（string / []byte / io.Reader / 任意可 JSON 编码的值）
func (builder *Http) WithBody(body any) *Http {
	builder.optionList = append(builder.optionList, WithBody(body))
	return builder
}

// WithJSON 把结构体或 map 编码为 JSON 请求体
func (builder *Http) WithJSON(body any) *Http {
	builder.optionList = append(builder.optionList, WithJSON(body))
	return builder
}

// WithForm 把 map[string]interface{} / map[string]string / url.Values 编码为表单请求体
func (builder *Http) WithForm(form any) *Http {
	builder.optionList = append(builder.optionList, WithForm(form))
	return builder
}

// Decode 指定响应体解析方式（DecodeJSON / DecodeXML）
func (builder *Http) Decode(decode string) *Http {
	builder.optionList = append(builder.optionList, Decode(decode))
	return builder
}

// Host 强制连接指定 IP（等价 cURL CURLOPT_RESOLVE），port 省略时沿用 URL 端口
func (builder *Http) Host(ip string, port ...int64) *Http {
	builder.optionList = append(builder.optionList, Host(ip, port...))
	return builder
}

// WithClient 临时替换底层 http.Client
func (builder *Http) WithClient(client *Client) *Http {
	builder.optionList = append(builder.optionList, WithClient(client))
	return builder
}

// WithContext 指定本次请求的 context（取消 / 链路传递）
func (builder *Http) WithContext(ctx context.Context) *Http {
	builder.optionList = append(builder.optionList, WithContext(ctx))
	return builder
}

// Reset 清空已累积的配置，便于复用同一个构建器发另一套参数的请求
func (builder *Http) Reset() *Http {
	builder.optionList = nil
	return builder
}

// Get 发起 GET 请求，返回 *Response（错误见 Response.Error）
func (builder *Http) Get(rawURL string, optionList ...Option) *Response {
	return builder.Do(http.MethodGet, rawURL, optionList...)
}

// Post 发起 POST 请求，返回 *Response（错误见 Response.Error）
func (builder *Http) Post(rawURL string, optionList ...Option) *Response {
	return builder.Do(http.MethodPost, rawURL, optionList...)
}

// Do 用指定方法发起请求：构建器上的配置在前，单次传入的 optionList 在后（后者可覆盖前者）
func (builder *Http) Do(method string, rawURL string, optionList ...Option) *Response {
	mergedList := make([]Option, 0, len(builder.optionList)+len(optionList))
	mergedList = append(mergedList, builder.optionList...)
	mergedList = append(mergedList, optionList...)
	return doRequest(method, rawURL, mergedList...)
}

// Package http 是基于标准库 net/http 的 HTTP 客户端封装，开箱即用：
//   - Get / Post 便捷入口，配合函数式 Option 逐请求配置
//   - 超时与取消（context）、URL Query 合并、批量请求头、JSON / 表单 / 原始请求体
//   - Host 强制指定连接 IP（等价 cURL CURLOPT_RESOLVE），且连接池仍可复用
//   - 状态码判定正常 / 异常（Allow 可追加 200 之外的正常码），响应体自动关闭
//   - 响应体一键反序列化（按 Content-Type 推断 JSON / XML），可选日志回调接入自己的日志体系
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const StatusOK = 200 // RFC 9110, 15.3.1

// userAgent 未显式指定 User-Agent 时的默认请求头
const userAgent = "GoHttpClient/1.0.0"

// DefaultTimeout 未通过 Timeout 显式指定时的单请求超时
const DefaultTimeout = 10 * time.Second

// DefaultContentType 请求体非空且未通过 ContentType 显式指定时的默认 Content-Type。
// 多数业务接口以 JSON 为主，故默认按 JSON 处理；表单等特殊场景用 WithForm 或 ContentType 覆盖。
const DefaultContentType = "application/json;charset=UTF-8"

// 响应体的解析方式（对应 Decode 选项与 Response.Json）。
// 少数接口返回 XML：请求时传 Decode(DecodeXML)，Response.Json 会改用 XML 解析。
const (
	DecodeJSON = "json"
	DecodeXML  = "xml"
)

// allowCode 默认视为「正常」的响应状态码（200）。
// 少量接口用 204/201 等表示成功，可用 Allow(204) 在默认基础上追加，见 Response.IsWrong。
var allowCode = []int{StatusOK}

// logHeaderLimit 日志里响应头 JSON 的最大长度；超出部分截断，
// 避免目标端返回大量响应头（Set-Cookie、链路追踪头等）把调试日志撑得过长。
const logHeaderLimit = 1000

// DefaultClient 所有对外请求共用的底层客户端（连接与连接池被复用）。
// 超时/取消通过每次请求的 context 控制，可逐请求单独指定；也可用 WithClient 临时替换。
var DefaultClient = &http.Client{}

// options 一次请求的配置，由各 Option 填充
type options struct {
	client      *http.Client
	ctx         context.Context
	timeout     time.Duration
	headers     map[string]string
	query       map[string]string
	contentType string
	body        []byte
	host        string       // 强制连接的 IP（等价 cURL CURLOPT_RESOLVE），见 Host 选项；为空表示按域名解析
	port        int64        // 与 host 配套的目标端口；<=0 时沿用 URL 原端口
	decode      string       // 响应体解析方式，见 DecodeJSON / DecodeXML；为空表示按响应头推断，默认 JSON
	allowCodes  map[int]bool // 视为「正常」的状态码，默认含 allowCode，可用 Allow 追加
	logCall     LogFunc      // 日志回调，见 Debug 选项；为 nil 时本次请求不记录日志
	encodeErr   error        // 请求体编码阶段的错误（Option 内部无法抛错，暂存后由 doRequest 统一返回）
}

// Option 请求配置项，通过函数方式追加配置
type Option func(*options)

// LogFunc 请求日志回调：title 为日志标题，args 为要记录的内容。
// 通过 Debug 选项注入；未注入时本次请求不记录日志。
type LogFunc func(title string, args ...any)

// record 记录一条请求日志：通过 Debug 注入回调时调用之，未注入则不记录。
func (cfg *options) record(title string, args ...any) {
	if cfg.logCall != nil {
		cfg.logCall(title, args...)
	}
}

// requestInfo 一次对外请求的调试摘要，供 doRequest 各分支在 return 前记录：
// 请求侧固定含 Method/Url/Headers/Body（发送的数据）；
// 已经发出请求后用 withResponse 补上 StatusCode/ResponseHeader/RawResponse（收到的原始响应内容）。
type requestInfo struct {
	Method         string            `json:"method"`
	Url            string            `json:"url"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`     // 发送的数据原文
	RemoteIP       string            `json:"remoteIP,omitempty"` // 目标服务器的 IP（httptrace 捕获）
	StatusCode     int               `json:"statusCode,omitempty"`
	ResponseHeader string            `json:"responseHeader,omitempty"` // 响应头的紧凑 JSON，超长会截断（见 logHeaderLimit）
	RawResponse    string            `json:"rawResponse,omitempty"`    // 收到的响应体原文
	Used           string            `json:"used,omitempty"`           // 请求耗时
	Error          string            `json:"error,omitempty"`          // 失败原因（成功时为空）
}

// newRequestInfo 组装请求侧摘要（method 为空按 GET 记）
func newRequestInfo(method string, requestURL string, cfg *options) *requestInfo {
	if method == "" {
		method = http.MethodGet
	}
	info := &requestInfo{Method: method, Url: requestURL}
	if cfg == nil {
		return info
	}
	if len(cfg.headers) > 0 {
		info.Headers = cfg.headers
	}
	if len(cfg.body) > 0 {
		info.Body = string(cfg.body)
	}
	return info
}

// withResponse 补上已收到的响应信息（状态码、响应头、原始响应体、耗时）
func (info *requestInfo) withResponse(result *Response) *requestInfo {
	if info == nil || result == nil {
		return info
	}
	info.RemoteIP = result.RemoteIP
	info.StatusCode = result.StatusCode
	info.ResponseHeader = headerJSON(result.Header)
	info.RawResponse = result.Html() // 原始响应内容（[]byte 直接 JSON 会变 base64，这里按文本记）
	info.Used = result.Used.String()
	return info
}

// headerJSON 把响应头序列化成紧凑 JSON 文本；长度超过 logHeaderLimit 时截断并标注完整长度。
// 序列化失败时降级为 fmt.Sprint，保证日志仍能记到内容。
func headerJSON(header http.Header) string {
	if len(header) == 0 {
		return ""
	}
	headerBytes, marshalErr := json.Marshal(header)
	if marshalErr != nil {
		return fmt.Sprint(header)
	}
	headerText := string(headerBytes)
	// 按字符（rune）截断，避免把中文等多字节字符切成半个导致乱码
	headerRunes := []rune(headerText)
	if len(headerRunes) <= logHeaderLimit {
		return headerText
	}
	return string(headerRunes[:logHeaderLimit]) + fmt.Sprintf("...(已截断，完整长度 %d 字符)", len(headerRunes))
}

// ipFromAddr 从 "1.2.3.4:443" 形式的地址里取出 IP 部分；拿不到时退回原始地址文本。
func ipFromAddr(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if tcpAddr, isTCPAddr := addr.(*net.TCPAddr); isTCPAddr {
		return tcpAddr.IP.String()
	}
	host, _, splitErr := net.SplitHostPort(addr.String())
	if splitErr != nil {
		return addr.String()
	}
	return host
}

// withError 附加上错误信息
func (info *requestInfo) withError(requestErr error) *requestInfo {
	if info == nil || requestErr == nil {
		return info
	}
	info.Error = requestErr.Error()
	return info
}

// doRequest 发起一次 HTTP 请求（通用出口，Get/Post 均为它的便捷封装）。
// method 为 GET/POST/PUT/DELETE 等；rawURL 可直接携带 QueryString。
// 返回 *Response（出错时也可能有值，含已解析的状态），以及可能发生的错误：
//   - 请求失败/超时错误：可用 IsTimeout 判断是否超时；
//   - 响应体读取失败等。
func doRequest(method string, rawURL string, optionList ...Option) (*Response, error) {
	cfg := options{
		ctx:     context.Background(),
		timeout: DefaultTimeout,
		headers: map[string]string{
			"User-Agent": userAgent,
		},
	}
	for _, apply := range optionList {
		apply(&cfg)
	}
	// 允许的状态码：默认允许 allowCode（200），Allow 选项可在此之外追加 204/201 等
	if cfg.allowCodes == nil {
		cfg.allowCodes = make(map[int]bool, len(allowCode))
	}
	for _, code := range allowCode {
		cfg.allowCodes[code] = true
	}

	if cfg.encodeErr != nil {
		cfg.record("HttpRequestError: 请求体编码失败", newRequestInfo(method, rawURL, &cfg).withError(cfg.encodeErr))
		return nil, cfg.encodeErr
	}

	// 有请求体但未显式指定 Content-Type 时，按默认 JSON 发送
	if cfg.contentType == "" && len(cfg.body) > 0 {
		cfg.contentType = DefaultContentType
	}

	// 1. 组装 URL（追加 Query）
	parsedURL, urlErr := url.Parse(rawURL)
	if urlErr != nil {
		requestErr := fmt.Errorf("URL 格式错误 %q: %v", rawURL, urlErr)
		cfg.record("HttpRequestError: URL 格式错误", newRequestInfo(method, rawURL, &cfg).withError(requestErr))
		return nil, requestErr
	}
	if len(cfg.query) > 0 {
		queryValues := parsedURL.Query()
		for queryKey, queryValue := range cfg.query {
			queryValues.Set(queryKey, queryValue)
		}
		parsedURL.RawQuery = queryValues.Encode()
	}

	// 2. 超时控制（通过 context，不替换共享 client，连接池仍可复用）
	if cfg.timeout > 0 {
		var cancel context.CancelFunc
		cfg.ctx, cancel = context.WithTimeout(cfg.ctx, cfg.timeout)
		defer cancel()
	}

	if method == "" {
		method = http.MethodGet
	}

	// 3. 创建请求（挂上 httptrace，捕获本次连接对端的 IP；连接复用时也会回调 GotConn）
	var remoteIP atomic.Value
	requestContext := httptrace.WithClientTrace(cfg.ctx, &httptrace.ClientTrace{
		GotConn: func(connInfo httptrace.GotConnInfo) {
			if connInfo.Conn == nil {
				return
			}
			remoteIP.Store(ipFromAddr(connInfo.Conn.RemoteAddr()))
		},
	})

	var requestBody io.Reader
	if len(cfg.body) > 0 {
		requestBody = bytes.NewReader(cfg.body)
	}
	request, reqErr := http.NewRequestWithContext(requestContext, method, parsedURL.String(), requestBody)
	if reqErr != nil {
		requestErr := fmt.Errorf("创建请求失败: %v", reqErr)
		cfg.record("HttpRequestError: 创建请求失败", newRequestInfo(method, parsedURL.String(), &cfg).withError(requestErr))
		return nil, requestErr
	}

	// 4. 请求头（Host 头需单独设置到 request.Host，直接放 Header 会被 Go 忽略）
	for headerName, headerValue := range cfg.headers {
		if strings.EqualFold(headerName, "Host") {
			request.Host = headerValue
			continue
		}
		request.Header.Set(headerName, headerValue)
	}
	if cfg.contentType != "" {
		request.Header.Set("Content-Type", cfg.contentType)
	}

	// 5. 发送并读取响应
	client := DefaultClient
	if cfg.client != nil {
		client = cfg.client
	}
	// 指定了目标 IP 时换用定向解析的客户端（等价 cURL CURLOPT_RESOLVE）
	if cfg.host != "" {
		client = resolveClient(client, cfg.host, cfg.port)
	}
	result := &Response{Start: time.Now().UnixMilli(), allowCodes: cfg.allowCodes}
	response, doErr := client.Do(request)
	result.Used = time.Since(time.UnixMilli(result.Start))
	if connectedIP, hasIP := remoteIP.Load().(string); hasIP {
		result.RemoteIP = connectedIP
	}
	if doErr != nil {
		// 少数场景（重定向被拒、读取响应时出错等）会同时返回响应与错误，
		// 此时响应体需由调用方关闭，避免连接泄漏。
		if response != nil {
			result.StatusCode = response.StatusCode
			result.IsWrong = !result.allow(result.StatusCode)
			result.Status = response.Status
			result.Header = response.Header
			if response.Body != nil {
				_ = response.Body.Close()
			}
		}
		requestErr := classifyError(doErr)
		cfg.record("HttpRequestError: 请求发送失败", newRequestInfo(method, parsedURL.String(), &cfg).withResponse(result).withError(requestErr))
		return result, requestErr
	}
	// 正常路径：响应体读完后即关闭，Response.Body 是已读完的字节，调用方无需再关。
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(response.Body)

	result.StatusCode = response.StatusCode
	result.IsWrong = !result.allow(result.StatusCode) // 状态码不在允许列表内（默认只 200，Allow 可追加）即视为异常
	result.Status = response.Status
	result.Header = response.Header
	// 解析方式：优先取 Decode 选项；未指定时按响应头 Content-Type 推断（application/xml、text/xml 等）
	result.Decode = cfg.decode
	if result.Decode == "" {
		result.Decode = DecodeJSON
		if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "xml") {
			result.Decode = DecodeXML
		}
	}
	bodyBytes, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		requestErr := fmt.Errorf("读取响应失败: %v", readErr)
		cfg.record("HttpRequestError: 读取响应失败", newRequestInfo(method, parsedURL.String(), &cfg).withResponse(result).withError(requestErr))
		return result, requestErr
	}
	result.Body = bodyBytes

	// 完整信息：请求（URL/请求头/发送的数据）+ 响应（状态码/响应头/原始响应内容/耗时）
	cfg.record("HttpResponse", newRequestInfo(method, parsedURL.String(), &cfg).withResponse(result))

	return result, nil
}

// IsTimeout 判断错误是否为请求超时（网络层超时或 context 超时），
// 便于调用方对 Get/Post 返回的错误做针对性处理。
func IsTimeout(requestErr error) bool {
	if requestErr == nil {
		return false
	}
	var netError net.Error
	return errors.As(requestErr, &netError) && netError.Timeout()
}

// classifyError 把 client.Do 的错误整理成带上下文的错误
func classifyError(requestErr error) error {
	if IsTimeout(requestErr) {
		return fmt.Errorf("请求 Http 超时: %v", requestErr)
	}
	return fmt.Errorf("http 响应错误: %v", requestErr)
}

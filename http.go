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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const StatusOK = 200 // RFC 9110, 15.3.1

// userAgent 未显式指定 User-Agent 时的默认请求头
const userAgent = "GoHttpClient/0.1.5"

// DefaultTimeout 未通过 Timeout 显式指定时的单请求超时
const DefaultTimeout = 10 * time.Second

// DefaultContentType 请求体非空且未通过 ContentType 显式指定时的默认 Content-Type。
// 多数业务接口以 JSON 为主，故默认按 JSON 处理；表单等特殊场景用 WithForm 或 ContentType 覆盖。
const DefaultContentType = "application/json;charset=UTF-8"
const FormContentType = "application/x-www-form-urlencoded;charset=UTF-8"

// 响应体的解析方式（对应 Decode 选项与 Response.Json）。
// 少数接口返回 XML：请求时传 Decode(DecodeXML)，Response.Json 会改用 XML 解析。
const (
	DecodeJSON = "json"
	DecodeXML  = "xml"
)

// allowCode 默认视为「正常」的响应状态码（200）。
// 少量接口用 204/201 等表示成功，可用 Allow(204) 在默认基础上追加，见 Response.IsWrong。
var allowCode = []int{StatusOK}

// DefaultClient 所有对外请求共用的默认客户端构建器（连接与连接池被复用）。
// 需要自定义连接池、代理、TLS 等时，替换成 NewClient() 链式配置后的构建器：
//
//	http.DefaultClient = http.NewClient().MaxIdleConnsPerHost(50).IdleConnTimeout(45 * time.Second)
//
// 超时/取消通过每次请求的 context 控制，可逐请求单独指定；也可用 WithClient 临时替换。
var DefaultClient = NewClient()

// options 一次请求的配置，由各 Option 填充
type options struct {
	client      *Client // 客户端构建器，发请求前才 Build 出底层 http.Client
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
	encodeErr   error        // 请求体编码阶段的错误（Option 内部无法抛错，暂存后由 doRequest 统一返回）
}

// Option 请求配置项，通过函数方式追加配置
type Option func(*options)

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

// portFromAddr 从 "1.2.3.4:443" 形式的地址里取出端口；拿不到时返回 0。
func portFromAddr(addr net.Addr) int {
	if addr == nil {
		return 0
	}
	if tcpAddr, isTCPAddr := addr.(*net.TCPAddr); isTCPAddr {
		return tcpAddr.Port
	}
	_, portText, splitErr := net.SplitHostPort(addr.String())
	if splitErr != nil {
		return 0
	}
	port, atoiErr := strconv.Atoi(portText)
	if atoiErr != nil {
		return 0
	}
	return port
}

// doRequest 发起一次 HTTP 请求（通用出口，Get/Post 均为它的便捷封装）。
// method 为 GET/POST/PUT/DELETE 等；rawURL 可直接携带 QueryString。
// 返回 *Response（出错时也可能有值，含已解析的状态），以及可能发生的错误：
//   - 请求失败/超时错误：可用 IsTimeout 判断是否超时；
//   - 响应体读取失败等。
func doRequest(method string, rawURL string, optionList ...Option) *Response {
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
		return &Response{Error: cfg.encodeErr}
	}

	// 有请求体但未显式指定 Content-Type 时，按默认 JSON 发送
	if cfg.contentType == "" && len(cfg.body) > 0 {
		cfg.contentType = DefaultContentType
	}

	// 1. 组装 URL（追加 Query）
	parsedURL, urlErr := url.Parse(rawURL)
	if urlErr != nil {
		requestErr := fmt.Errorf("URL 格式错误 %q: %v", rawURL, urlErr)
		return &Response{Error: requestErr}
	}
	if len(cfg.query) > 0 {
		queryValues := parsedURL.Query()
		for queryKey, queryValue := range cfg.query {
			queryValues.Set(queryKey, queryValue)
		}
		parsedURL.RawQuery = queryValues.Encode()
	}
	reqUrl := parsedURL.String()

	// 2. 超时控制（通过 context，不替换共享 client，连接池仍可复用）
	if cfg.timeout > 0 {
		var cancel context.CancelFunc
		cfg.ctx, cancel = context.WithTimeout(cfg.ctx, cfg.timeout)
		defer cancel()
	}

	if method == "" {
		method = http.MethodGet
	}

	// 3. 创建请求（挂上 httptrace，捕获本次连接对端的 IP 与端口；连接复用时也会回调 GotConn）
	var remoteIP atomic.Value
	var remotePort atomic.Value
	requestContext := httptrace.WithClientTrace(cfg.ctx, &httptrace.ClientTrace{
		GotConn: func(connInfo httptrace.GotConnInfo) {
			if connInfo.Conn == nil {
				return
			}
			remoteIP.Store(ipFromAddr(connInfo.Conn.RemoteAddr()))
			remotePort.Store(portFromAddr(connInfo.Conn.RemoteAddr()))
		},
	})

	var requestBody io.Reader
	if len(cfg.body) > 0 {
		requestBody = bytes.NewReader(cfg.body)
	}
	request, reqErr := http.NewRequestWithContext(requestContext, method, reqUrl, requestBody)
	if reqErr != nil {
		requestErr := fmt.Errorf("创建请求失败: %v", reqErr)
		return &Response{Error: requestErr}
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

	// 5. 发送并读取响应：此处才真正创建（或复用已缓存的）底层 http.Client
	clientBuilder := DefaultClient
	if cfg.client != nil {
		clientBuilder = cfg.client
	}
	requestClient := clientBuilder.Build()
	// 指定了目标 IP 时换用定向解析的客户端（等价 cURL CURLOPT_RESOLVE）
	if cfg.host != "" {
		requestClient = resolveClient(requestClient, cfg.host, cfg.port)
	}
	result := &Response{
		Start:      time.Now().UnixMilli(),
		allowCodes: cfg.allowCodes,
		Url:        reqUrl,
		Method:     method,
		ReqHeaders: request.Header,
	}
	// 带请求体的请求（如 POST）把发送的原文记下来，便于日志排查
	if len(cfg.body) > 0 {
		result.Data = string(cfg.body)
	}
	response, doErr := requestClient.Do(request)
	result.Used = time.Since(time.UnixMilli(result.Start))
	if connectedIP, hasIP := remoteIP.Load().(string); hasIP {
		result.RemoteIP = connectedIP
	}
	if connectedPort, hasPort := remotePort.Load().(int); hasPort {
		result.RemotePort = int64(connectedPort)
	}
	if doErr != nil {
		// 少数场景（重定向被拒、读取响应时出错等）会同时返回响应与错误，
		// 此时响应体需由调用方关闭，避免连接泄漏。
		if response != nil {
			result.StatusCode = response.StatusCode
			result.IsWrong = !result.allow(result.StatusCode)
			result.Status = response.Status
			result.ResHeaders = response.Header
			if response.Body != nil {
				_ = response.Body.Close()
			}
		}
		requestErr := classifyError(doErr)
		result.Error = requestErr
		return result
	}
	// 正常路径：响应体读完后即关闭，Response.Body 是已读完的字节，调用方无需再关。
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(response.Body)

	result.StatusCode = response.StatusCode
	result.IsWrong = !result.allow(result.StatusCode) // 状态码不在允许列表内（默认只 200，Allow 可追加）即视为异常
	result.Status = response.Status
	result.ResHeaders = response.Header
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
		result.Error = requestErr
		return result
	}
	result.Body = bodyBytes

	return result
}

// Map 快速构造 map[string]interface{}，省去逐个写类型的麻烦。
// 按「键、值」成对传入，键不是字符串时该对会被忽略（奇数个参数时最后一项忽略）：
//
//	WithQuery(Map("page", 1, "size", 20))              // map[page:1 size:20]
//	Headers(Map("X-Token", "abc", "Accept", "json"))   // map[X-Token:abc Accept:json]
//	WithForm(Map("user", "tom", "pass", "123456"))     // 表单体
func Map(pairs ...interface{}) map[string]interface{} {
	params := make(map[string]interface{}, len(pairs)/2)
	for index := 0; index < len(pairs)-1; index += 2 {
		field, ok := pairs[index].(string)
		if !ok {
			continue
		}
		params[field] = pairs[index+1]
	}
	return params
}

// valueText 把 Map 传入的任意类型值转成字符串，nil 转为空字符串。
// 供 WithQuery / Headers / WithForm 等接受 map[string]interface{} 的选项使用。
func valueText(value interface{}) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

// IsTimeout 判断错误是否为请求超时（网络层超时或 context 超时），
// 便于调用方对 Get/Post 返回的错误做针对性处理。
// 同时兼容 context.DeadlineExceeded：错误被第三方包装、错误链中断时也能识别。
func IsTimeout(requestErr error) bool {
	if requestErr == nil {
		return false
	}
	if errors.Is(requestErr, context.DeadlineExceeded) {
		return true
	}
	var netError net.Error
	return errors.As(requestErr, &netError) && netError.Timeout()
}

// classifyError 把 client.Do 的错误整理成带上下文的错误。
// 必须用 %w 保留错误链，否则调用方的 errors.Is / errors.As（含 IsTimeout）无法穿透到原始错误。
func classifyError(requestErr error) error {
	if IsTimeout(requestErr) {
		return fmt.Errorf("请求 Http 超时: %w", requestErr)
	}
	return fmt.Errorf("http 响应错误: %w", requestErr)
}

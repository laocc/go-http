package http

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"
)

// Response 一次 HTTP 请求的完整响应。
// Body 是读取完成的响应体原文，已自动关闭底层连接。
type Response struct {
	Error      error
	IsWrong    bool                // 响应是否异常：状态码不在允许列表内为 true（默认只允许 200，见 Allow 选项）
	Url        string              // 目标 URL
	Method     string              // 请求方式，post get
	ReqHeaders map[string][]string //发送出去的请求头
	StatusCode int                 // HTTP 状态码，200 表示正常
	Status     string              // 状态文本，如 "200 OK"
	ResHeaders http.Header         // 收到的响应头
	Body       []byte              // 响应体原文
	Decode     string              // 响应体解析方式：DecodeJSON / DecodeXML（由请求选项 Decode 或响应头推断）
	Start      int64               // 请求开始时间（Unix 毫秒）
	Used       time.Duration       // 请求耗时
	RemoteIP   string              // 目标服务器的 IP（由 httptrace 捕获，连接复用时也有；DNS 失败等未连通场景为空）
	allowCodes map[int]bool        // 视为「正常」的状态码（默认含 200，可用 Allow 追加），由请求配置带入
}

// allow 判断状态码是否在允许列表内；列表为空（如手工构造的 Response）时按默认只允许 200。
func (resp *Response) allow(statusCode int) bool {
	if len(resp.allowCodes) == 0 {
		return statusCode == StatusOK
	}
	return resp.allowCodes[statusCode]
}

// headerToMap 把 map[string][]string 形式的请求头/响应头压平成 map[string]string，便于日志阅读：
// 单个值直接取该值；多个值按 HTTP 惯例用 ", " 拼接；没有值则跳过该键。
func headerToMap(header map[string][]string) map[string]string {
	if len(header) == 0 {
		return nil
	}
	flatHeader := make(map[string]string, len(header))
	for name, values := range header {
		switch len(values) {
		case 0:
			continue
		case 1:
			flatHeader[name] = values[0]
		default:
			flatHeader[name] = strings.Join(values, ", ")
		}
	}
	return flatHeader
}

// DebugInfo 把本次响应序列化成分行缩进的 JSON 文本，可直接落日志。
func (resp *Response) DebugInfo() string {
	if resp == nil {
		return ""
	}
	view := struct {
		IsWrong    bool              `json:"isWrong"`
		Method     string            `json:"method,omitempty"`
		Url        string            `json:"url,omitempty"`
		RemoteIP   string            `json:"remoteIP,omitempty"`
		StatusCode int               `json:"statusCode"`
		Status     string            `json:"status,omitempty"`
		ReqHeaders map[string]string `json:"reqHeaders,omitempty"`
		ResHeaders map[string]string `json:"resHeaders,omitempty"`
		Body       string            `json:"body,omitempty"`
		BodySize   int               `json:"bodySize,omitempty"`
		Decode     string            `json:"decode,omitempty"`
		Start      string            `json:"start,omitempty"`
		Used       string            `json:"used,omitempty"`
		UsedMs     int64             `json:"usedMs,omitempty"`
	}{
		IsWrong:    resp.IsWrong,
		Method:     resp.Method,
		Url:        resp.Url,
		RemoteIP:   resp.RemoteIP,
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		ReqHeaders: headerToMap(resp.ReqHeaders),
		ResHeaders: headerToMap(resp.ResHeaders),
		Body:       string(resp.Body),
		BodySize:   len(resp.Body),
		Decode:     resp.Decode,
	}
	if resp.Start > 0 {
		view.Start = time.UnixMilli(resp.Start).Format("2006-01-02 15:04:05.000")
	}
	if resp.Used > 0 {
		view.Used = resp.Used.String()
		view.UsedMs = resp.Used.Milliseconds()
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)  // 不转义 < > &，与 PHP json_encode 行为一致
	encoder.SetIndent("", "    ") // 4 空格缩进，分行输出
	if encodeErr := encoder.Encode(view); encodeErr != nil {
		return ""
	}
	// Encode 会在末尾补一个换行，去掉它，由调用方决定要不要换行
	return strings.TrimRight(buffer.String(), "\n")
}

// Html 返回响应体文本
func (resp *Response) Html() string {
	if resp == nil {
		return ""
	}
	return string(resp.Body)
}

// Json 把响应体反序列化到 target（方法名沿用历史叫法，实际解析方式由 Response.Decode 决定）。
// 默认按 JSON 解析；请求时传了 Decode(DecodeXML)、或响应头 Content-Type 含 xml 时，改按 XML 解析。
// 响应体为空（或只有空白字符）时不视为错误，而是把 target 置为零值后返回 nil，
// 便于调用方用「target 是否为空」判断接口有无返回数据。
func (resp *Response) Json(target any) error {
	if resp == nil || len(bytes.TrimSpace(resp.Body)) == 0 {
		resetTarget(target)
		return nil
	}
	if strings.EqualFold(resp.Decode, DecodeXML) {
		if xmlErr := xml.Unmarshal(resp.Body, target); xmlErr != nil {
			return fmt.Errorf("解析 XML 响应失败: %v", xmlErr)
		}
		return nil
	}
	return json.Unmarshal(resp.Body, target)
}

// Xml 强制按 XML 解析响应体到 target（不受 Response.Decode 影响）。
// 响应体为空时同样把 target 置为零值并返回 nil。
func (resp *Response) Xml(target any) error {
	if resp == nil || len(bytes.TrimSpace(resp.Body)) == 0 {
		resetTarget(target)
		return nil
	}
	if xmlErr := xml.Unmarshal(resp.Body, target); xmlErr != nil {
		return fmt.Errorf("解析 XML 响应失败: %v", xmlErr)
	}
	return nil
}

// resetTarget 把 target 指向的值置为零值（指针置 nil、结构体字段清零、map/slice 置 nil）。
// target 为 nil 或非指针（无法写入）时直接跳过，不报错。
func resetTarget(target any) {
	if target == nil {
		return
	}
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return
	}
	value.Elem().Set(reflect.Zero(value.Elem().Type()))
}

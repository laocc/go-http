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
	IsWrong    bool          // 响应是否异常：状态码不在允许列表内为 true（默认只允许 200，见 Allow 选项）
	RemoteIP   string        // 目标服务器的 IP（由 httptrace 捕获，连接复用时也有；DNS 失败等未连通场景为空）
	StatusCode int           // HTTP 状态码，200 表示正常
	Status     string        // 状态文本，如 "200 OK"
	Header     http.Header   // 响应头
	Body       []byte        // 响应体原文
	Decode     string        // 响应体解析方式：DecodeJSON / DecodeXML（由请求选项 Decode 或响应头推断）
	Start      int64         // 请求开始时间（Unix 毫秒）
	Used       time.Duration // 请求耗时

	allowCodes map[int]bool // 视为「正常」的状态码（默认含 200，可用 Allow 追加），由请求配置带入
}

// allow 判断状态码是否在允许列表内；列表为空（如手工构造的 Response）时按默认只允许 200。
func (resp *Response) allow(statusCode int) bool {
	if len(resp.allowCodes) == 0 {
		return statusCode == StatusOK
	}
	return resp.allowCodes[statusCode]
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

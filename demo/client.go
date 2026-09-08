package main

import (
	"fmt"
	"time"

	req "github.com/laocc/go-http"
)

// ClientDefault 开箱即用：NewClient 已预置常用默认配置，大多数环境直接发请求即可。
// 默认包含：跟随环境代理、建连超时 30s、TLS 最低 1.2、连接池 100/100、空闲 90s、响应头等待 30s、开启 HTTP/2。
func ClientDefault() error {
	response := req.Get(demoURL+"/get", req.WithClient(req.NewClient()))
	if response.Error != nil {
		return response.Error
	}
	if response.IsWrong {
		return fmt.Errorf("请求失败: %d", response.StatusCode)
	}
	fmt.Println("默认配置请求:", response.StatusCode, "耗时:", response.Used)
	return nil
}

// ClientTuned 在默认配置之上按需微调：只覆盖需要的项，其余保持默认。
// 常见场景：并发量高时调大每域名空闲连接；网关空闲超时较短时调小 IdleConnTimeout。
func ClientTuned() error {
	client := req.NewClient().
		MaxIdleConnsPerHost(200).          // 并发高时调大（默认 100）
		IdleConnTimeout(45 * time.Second). // 小于服务端 keepalive 超时
		DialTimeout(5 * time.Second)       // 内网接口可缩短建连超时

	response := req.Get(demoURL+"/get", req.WithClient(client))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("微调后请求:", response.StatusCode, "耗时:", response.Used)
	return nil
}

// ClientLongWait 响应头返回很慢的接口：默认响应头等待 30s，不够时放宽（0 表示不限制）。
// 注意 ResponseHeaderTimeout 只作用于「发完请求到收到响应头」，不影响读取响应体的时间。
func ClientLongWait() error {
	client := req.NewClient().ResponseHeaderTimeout(0) // 需要多久都等
	response := req.Get(demoURL+"/delay/2",
		req.WithClient(client),
		req.Timeout(30*time.Second), // 整体超时仍由 Timeout 选项控制
	)
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("放宽响应头等待后请求:", response.StatusCode, "耗时:", response.Used)
	return nil
}

// ClientBuildOnce 验证惰性构建与缓存：配置未变时 Build 返回同一个底层客户端（连接池因此可复用），
// 改动任意配置后才会重建。
func ClientBuildOnce() error {
	client := req.NewClient()
	firstClient := client.Build()
	secondClient := client.Build()
	fmt.Println("配置未变，Build 返回同一实例:", firstClient == secondClient)

	client.MaxIdleConnsPerHost(20) // 改动配置
	rebuiltClient := client.Build()
	fmt.Println("改动配置后，Build 重建实例:", rebuiltClient != firstClient)

	// 实际发请求时也是复用同一个实例，连接池得以共享
	response := req.Get(demoURL+"/get", req.WithClient(client))
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("复用构建器请求:", response.StatusCode)
	return nil
}

// ClientDefaultGlobal 把默认配置的客户端设为全局：程序启动时设置一次，
// 之后所有不传 WithClient 的请求都会走它（会覆盖包内的默认 DefaultClient）。
//
// 注意：这会改变全局状态，示例中未加入 main 的默认调用，需要时手动打开。
func ClientDefaultGlobal() error {
	req.DefaultClient = req.NewClient().MaxIdleConnsPerHost(50)

	response := req.Get(demoURL + "/get") // 无需再传 WithClient
	if response.Error != nil {
		return response.Error
	}
	fmt.Println("全局默认客户端请求:", response.StatusCode)
	return nil
}

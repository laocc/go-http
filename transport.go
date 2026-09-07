package http

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"sync"
	"time"
)

// 未显式配置时使用的默认值。
// 其中 MaxIdleConnsPerHost 给到 100：标准库默认只有 2，高并发下会频繁建连、握手。
const (
	defaultDialTimeout           = 30 * time.Second
	defaultKeepAlive             = 30 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultIdleConnTimeout       = 90 * time.Second
	defaultExpectContinue        = 1 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
	defaultMaxIdleConns          = 100
	defaultMaxIdleConnsPerHost   = 100
	defaultBufferSize            = 32 * 1024
)

// Client 是 http.Client 的链式构建器：先逐项声明配置，真正发请求时（Build）才创建底层客户端。
//
// 用法：
//
//	client := http.NewClient().
//		MaxIdleConnsPerHost(50).
//		IdleConnTimeout(45 * time.Second).
//		HTTP2(true)
//
//	response := http.Get(url, http.WithClient(client)) // 此处才 Build
//
// 也可以设为全局默认：
//
//	http.DefaultClient = http.NewClient().MaxIdleConnsPerHost(50)
//
// 说明：
//  1. Build 的结果会被缓存，同一份配置只创建一次底层客户端，连接池因此得以复用；
//     再次调用配置方法会使缓存失效，下次 Build 重建。
//  2. 需要沿用已有客户端时用 From，Build 会直接返回它。
//  3. 构建期间的参数错误（如代理地址格式错误）不会中断请求，可用 Err 查询。
type Client struct {
	mutex     sync.Mutex
	buildErr  error
	rawClient *http.Client // 由 From 指定时，Build 直接返回它
	built     *http.Client

	// Transport：代理
	proxy              func(*http.Request) (*url.URL, error)
	proxyConnectHeader http.Header

	// Transport：拨号
	dialTimeout time.Duration
	keepAlive   time.Duration

	// Transport：TLS
	tlsConfig           *tls.Config
	tlsHandshakeTimeout time.Duration

	// Transport：连接池
	maxIdleConns        int
	maxIdleConnsPerHost int
	maxConnsPerHost     int
	idleConnTimeout     time.Duration
	disableKeepAlives   *bool
	disableCompression  *bool

	// Transport：超时与限制
	responseHeaderTimeout  time.Duration
	expectContinueTimeout  time.Duration
	maxResponseHeaderBytes int64
	writeBufferSize        int
	readBufferSize         int
	forceHTTP2             *bool

	// http.Client 层
	timeout       time.Duration // 整体超时；一般交给 Timeout 选项控制，这里保持 0
	jar           http.CookieJar
	checkRedirect func(*http.Request, []*http.Request) error
}

// NewClient 创建一个带常用默认配置的 http.Client 构建器，大多数环境可直接使用：
//
//   - 代理：跟随环境变量（HTTP_PROXY / HTTPS_PROXY / NO_PROXY），没配就是直连
//   - 建连：DialTimeout 30s、KeepAlive 30s
//   - TLS：最低 TLS 1.2、握手超时 10s
//   - 连接池：MaxIdleConns 100、MaxIdleConnsPerHost 100、空闲 90s（标准库默认仅 2，已调大）
//   - 超时：响应头等待 30s、Expect:100-continue 1s（整体超时仍交给 Timeout 选项控制）
//   - 协议：默认开启 HTTP/2、自动 gzip 解压、读写缓冲 32KB
//
// 需要微调时链式覆盖单项即可：
//
//	http.NewClient().MaxIdleConnsPerHost(200).IdleConnTimeout(45 * time.Second)
func NewClient() *Client {
	return &Client{
		// 代理：跟随环境变量，未配置时等价于直连
		proxy: http.ProxyFromEnvironment,

		// 建连
		dialTimeout: defaultDialTimeout,
		keepAlive:   defaultKeepAlive,

		// TLS：生产环境建议不低于 1.2
		tlsConfig:           &tls.Config{MinVersion: tls.VersionTLS12},
		tlsHandshakeTimeout: defaultTLSHandshakeTimeout,

		// 连接池
		maxIdleConns:        defaultMaxIdleConns,
		maxIdleConnsPerHost: defaultMaxIdleConnsPerHost,
		idleConnTimeout:     defaultIdleConnTimeout,
		disableKeepAlives:   boolPtr(false),

		// 超时与限制
		responseHeaderTimeout: defaultResponseHeaderTimeout,
		expectContinueTimeout: defaultExpectContinue,

		// 协议与缓冲
		forceHTTP2:         boolPtr(true),
		disableCompression: boolPtr(false),
		writeBufferSize:    defaultBufferSize,
		readBufferSize:     defaultBufferSize,
	}
}

// boolPtr 构造 *bool，用于区分「未设置」与「显式设为 false」
func boolPtr(value bool) *bool {
	return &value
}

// From 直接沿用已有的 http.Client，Build 时原样返回（其余配置不再生效）
func (client *Client) From(rawClient *http.Client) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.rawClient = rawClient
	client.built = nil
	return client
}

// Err 返回构建期间记录的错误（如代理地址解析失败）
func (client *Client) Err() error {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return client.buildErr
}

// Proxy 指定代理地址，如 "http://127.0.0.1:7890"；地址格式错误时记录到 Err
func (client *Client) Proxy(proxyURL string) *Client {
	parsedURL, parseErr := url.Parse(proxyURL)
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if parseErr != nil {
		client.buildErr = parseErr
		client.built = nil
		return client
	}
	client.proxy = http.ProxyURL(parsedURL)
	client.built = nil
	return client
}

// ProxyEnv 跟随环境变量（HTTP_PROXY / HTTPS_PROXY / NO_PROXY）
func (client *Client) ProxyEnv() *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.proxy = http.ProxyFromEnvironment
	client.built = nil
	return client
}

// ProxyFunc 自定义代理决策函数
func (client *Client) ProxyFunc(proxyFunc func(*http.Request) (*url.URL, error)) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.proxy = proxyFunc
	client.built = nil
	return client
}

// ProxyConnectHeader 设置 HTTPS 走 CONNECT 时附加的请求头（常用于代理鉴权）
func (client *Client) ProxyConnectHeader(header http.Header) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.proxyConnectHeader = header
	client.built = nil
	return client
}

// DialTimeout 建立 TCP 连接的超时（默认 30s）
func (client *Client) DialTimeout(dialTimeout time.Duration) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.dialTimeout = dialTimeout
	client.built = nil
	return client
}

// KeepAlive TCP 保活探测间隔（默认 30s，0 表示关闭保活）
func (client *Client) KeepAlive(keepAlive time.Duration) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.keepAlive = keepAlive
	client.built = nil
	return client
}

// TLSConfig 直接指定 tls.Config（证书、SNI、最低版本、客户端证书都可在此设置）
func (client *Client) TLSConfig(config *tls.Config) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.tlsConfig = config
	client.built = nil
	return client
}

// MinTLSVersion 设置最低 TLS 版本，如 tls.VersionTLS12（生产建议不低于 1.2）
func (client *Client) MinTLSVersion(version uint16) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.tlsConfig = client.ensureTLS().tlsConfig
	client.tlsConfig.MinVersion = version
	client.built = nil
	return client
}

// InsecureSkipVerify 跳过证书校验（仅测试环境，存在中间人风险）
func (client *Client) InsecureSkipVerify() *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.tlsConfig = client.ensureTLS().tlsConfig
	client.tlsConfig.InsecureSkipVerify = true
	client.built = nil
	return client
}

// RootCA 信任指定的 CA 根证书文件（内网证书、自签证书场景）
func (client *Client) RootCA(caFile string) *Client {
	caBytes, readErr := os.ReadFile(caFile)
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if readErr != nil {
		client.buildErr = readErr
		return client
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caBytes) {
		client.buildErr = fmt.Errorf("CA 证书解析失败: %s", caFile)
		return client
	}
	client.tlsConfig = client.ensureTLS().tlsConfig
	client.tlsConfig.RootCAs = certPool
	client.built = nil
	return client
}

// ClientCert 双向 TLS 的客户端证书与私钥
func (client *Client) ClientCert(certFile string, keyFile string) *Client {
	certificate, loadErr := tls.LoadX509KeyPair(certFile, keyFile)
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if loadErr != nil {
		client.buildErr = loadErr
		return client
	}
	client.tlsConfig = client.ensureTLS().tlsConfig
	client.tlsConfig.Certificates = []tls.Certificate{certificate}
	client.built = nil
	return client
}

// ensureTLS 保证 tlsConfig 非空（调用方需已持锁）
func (client *Client) ensureTLS() *Client {
	if client.tlsConfig == nil {
		client.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return client
}

// TLSHandshakeTimeout TLS 握手超时（默认 10s）
func (client *Client) TLSHandshakeTimeout(handshakeTimeout time.Duration) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.tlsHandshakeTimeout = handshakeTimeout
	client.built = nil
	return client
}

// MaxIdleConns 连接池全局空闲连接上限（默认 100）
func (client *Client) MaxIdleConns(total int) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.maxIdleConns = total
	client.built = nil
	return client
}

// MaxIdleConnsPerHost 单个域名的空闲连接上限（默认 100，标准库默认仅 2）
func (client *Client) MaxIdleConnsPerHost(perHost int) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.maxIdleConnsPerHost = perHost
	client.built = nil
	return client
}

// MaxConnsPerHost 单个域名的连接总数上限（含正在使用），0 表示不限
func (client *Client) MaxConnsPerHost(perHost int) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.maxConnsPerHost = perHost
	client.built = nil
	return client
}

// IdleConnTimeout 空闲连接保留时长（默认 90s，建议小于服务端 keepalive 超时）
func (client *Client) IdleConnTimeout(idleTimeout time.Duration) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.idleConnTimeout = idleTimeout
	client.built = nil
	return client
}

// DisableKeepAlives 关闭长连接（每次请求新建连接，一般不推荐）
func (client *Client) DisableKeepAlives(disabled bool) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.disableKeepAlives = &disabled
	client.built = nil
	return client
}

// DisableCompression 关闭自动 gzip 解压（自行处理响应体）
func (client *Client) DisableCompression(disabled bool) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.disableCompression = &disabled
	client.built = nil
	return client
}

// HTTP2 是否显式启用 HTTP/2（默认 true）
func (client *Client) HTTP2(enabled bool) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.forceHTTP2 = &enabled
	client.built = nil
	return client
}

// ResponseHeaderTimeout 发完请求到收到响应头的等待上限（不含读取响应体）
func (client *Client) ResponseHeaderTimeout(headerTimeout time.Duration) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.responseHeaderTimeout = headerTimeout
	client.built = nil
	return client
}

// ExpectContinueTimeout 发送 Expect: 100-continue 后等待服务端确认的时长
func (client *Client) ExpectContinueTimeout(continueTimeout time.Duration) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.expectContinueTimeout = continueTimeout
	client.built = nil
	return client
}

// MaxResponseHeaderBytes 响应头最大字节数（0 用标准库默认 1MB）
func (client *Client) MaxResponseHeaderBytes(maxBytes int64) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.maxResponseHeaderBytes = maxBytes
	client.built = nil
	return client
}

// WriteBufferSize 写缓冲区大小（默认 32KB）
func (client *Client) WriteBufferSize(bufferSize int) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.writeBufferSize = bufferSize
	client.built = nil
	return client
}

// ReadBufferSize 读缓冲区大小（默认 32KB）
func (client *Client) ReadBufferSize(bufferSize int) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.readBufferSize = bufferSize
	client.built = nil
	return client
}

// Timeout 设置 http.Client 的整体超时。
// 一般不需要：本库的 Timeout 选项通过 context 控制，与它同时设置时谁短谁生效。
func (client *Client) Timeout(requestTimeout time.Duration) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.timeout = requestTimeout
	client.built = nil
	return client
}

// CookieJar 指定 Cookie 管理器
func (client *Client) CookieJar(jar http.CookieJar) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.jar = jar
	client.built = nil
	return client
}

// UseCookieJar 启用默认的 Cookie 管理器，自动维护登录态
func (client *Client) UseCookieJar() *Client {
	jar, jarErr := cookiejar.New(nil)
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if jarErr != nil {
		client.buildErr = jarErr
		return client
	}
	client.jar = jar
	client.built = nil
	return client
}

// Redirect 自定义重定向策略；返回 nil 表示跟随，返回 http.ErrUseLastResponse 表示不跟随
func (client *Client) Redirect(redirectFunc func(*http.Request, []*http.Request) error) *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.checkRedirect = redirectFunc
	client.built = nil
	return client
}

// NoRedirect 不跟随重定向，直接拿到 30x 响应
func (client *Client) NoRedirect() *Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	client.checkRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	client.built = nil
	return client
}

// Build 按当前配置创建最终的 http.Client。
// 结果会被缓存：配置未变时多次调用返回同一实例，保证连接池可复用。
func (client *Client) Build() *http.Client {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if client.built != nil {
		return client.built
	}
	if client.rawClient != nil {
		client.built = client.rawClient
		return client.built
	}

	transport := &http.Transport{
		Proxy:              client.proxy,
		ProxyConnectHeader: client.proxyConnectHeader,
		DialContext: (&net.Dialer{
			Timeout:   durationOr(client.dialTimeout, defaultDialTimeout),
			KeepAlive: durationOr(client.keepAlive, defaultKeepAlive),
		}).DialContext,
		TLSClientConfig:        client.tlsConfig,
		TLSHandshakeTimeout:    durationOr(client.tlsHandshakeTimeout, defaultTLSHandshakeTimeout),
		MaxIdleConns:           intOr(client.maxIdleConns, defaultMaxIdleConns),
		MaxIdleConnsPerHost:    intOr(client.maxIdleConnsPerHost, defaultMaxIdleConnsPerHost),
		MaxConnsPerHost:        client.maxConnsPerHost,
		IdleConnTimeout:        durationOr(client.idleConnTimeout, defaultIdleConnTimeout),
		ResponseHeaderTimeout:  client.responseHeaderTimeout,
		ExpectContinueTimeout:  durationOr(client.expectContinueTimeout, defaultExpectContinue),
		MaxResponseHeaderBytes: client.maxResponseHeaderBytes,
		WriteBufferSize:        intOr(client.writeBufferSize, defaultBufferSize),
		ReadBufferSize:         intOr(client.readBufferSize, defaultBufferSize),
		DisableKeepAlives:      boolOr(client.disableKeepAlives, false),
		DisableCompression:     boolOr(client.disableCompression, false),
		ForceAttemptHTTP2:      boolOr(client.forceHTTP2, true),
	}

	client.built = &http.Client{
		Transport:     transport,
		Timeout:       client.timeout,
		Jar:           client.jar,
		CheckRedirect: client.checkRedirect,
	}
	return client.built
}

// durationOr 取首个大于 0 的值，否则用默认值
func durationOr(value time.Duration, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

// intOr 取首个大于 0 的值，否则用默认值
func intOr(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

// boolOr 三态布尔：未设置时取默认值
func boolOr(flag *bool, fallback bool) bool {
	if flag == nil {
		return fallback
	}
	return *flag
}

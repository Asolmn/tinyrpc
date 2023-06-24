package xclient

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// TinyRegistryDiscory 带有注册中心的服务端发现实例
type TinyRegistryDiscory struct {
	*MultiServerDiscovery               // 嵌套没有注册中心的服务发现，方便复用
	registry              string        // 注册中心的地址
	timeout               time.Duration // 服务列表的过期时间
	lastUpdate            time.Time     // 最后从注册中心更新服务列表的时间，默认是10s
}

// 默认注册中心服务列表过期时间
const defaultUpdateTimeout = time.Second * 10

// NewTinyRegistryDiscovery 初始化服务端发现实例
func NewTinyRegistryDiscovery(registerAddr string, timeout time.Duration) *TinyRegistryDiscory {
	// 设置服务列表的过期时间
	if timeout == 0 {
		timeout = defaultUpdateTimeout
	}

	// 初始化实例
	d := &TinyRegistryDiscory{
		MultiServerDiscovery: NewMultiServerDiscovery(make([]string, 0)),
		registry:             registerAddr,
		timeout:              timeout,
	}
	return d
}

// Update 手动更新服务列表
func (d *TinyRegistryDiscory) Update(servers []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.servers = servers
	d.lastUpdate = time.Now()

	return nil
}

// Refresh 从注册中心更新服务列表
func (d *TinyRegistryDiscory) Refresh() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 检查最后的更新时间是否超过设置默认更新时间间隔
	if d.lastUpdate.Add(d.timeout).After(time.Now()) {
		return nil
	}

	// 向注册中心发送get请求，获取响应
	log.Println("rpc registry: refresh servers from register", d.registry)
	resp, err := http.Get(d.registry)

	// 检验更新服务列表情况
	if err != nil {
		log.Println("rpc registry refresh err: ", resp)
		return err
	}

	// 获取服务端列表
	servers := strings.Split(resp.Header.Get("X-Tinyrpc-Servers"), ",")
	// 初始化服务发现实例中的服务列表
	d.servers = make([]string, 0, len(servers))

	// 遍历从响应中获取的服务端列表
	for _, server := range servers {
		if strings.TrimSpace(server) != "" { // 如果服务端地址不为空
			// 添加到服务发现列表
			d.servers = append(d.servers, strings.TrimSpace(server))
		}
	}
	// 更新设置服务列表的时间
	d.lastUpdate = time.Now()
	return nil
}

// Get 根据负载策略，返回一个服务端地址
func (d *TinyRegistryDiscory) Get(mode SelectMode) (string, error) {
	// 从注册中心更新服务端列表
	if err := d.Refresh(); err != nil {
		return "", err
	}
	// 根据负载策略，返回一个服务端
	return d.MultiServerDiscovery.Get(mode)
}

// GetAll 返回所有的服务实例
func (d *TinyRegistryDiscory) GetAll() ([]string, error) {
	// 从注册中心更新服务端列表
	if err := d.Refresh(); err != nil {
		return nil, err
	}
	// 返回服务端列表
	return d.MultiServerDiscovery.GetAll()
}

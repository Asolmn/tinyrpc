// Discovery mode

package xclient

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"
)

type SelectMode int // 表示不同的负载均衡策略

// 只实现Random和RoundRobin两种策略
const (
	RandomSelect     SelectMode = iota // Random选择
	RoundRobinSelect                   // Robbin算法选择
)

// Discovery 包含服务发现所需要的最基本的接口
type Discovery interface {
	Refresh() error                      // 从注册中心更新服务列表
	Update(servers []string) error       // 手动更新服务列表
	Get(mode SelectMode) (string, error) // 根据负载均衡策略，选择一个服务实例
	GetAll() ([]string, error)           // 返回所有服务实例
}

// MultiServerDiscovery 没有注册中心的多服务发现
// 用户改为显示提供服务器地址
type MultiServerDiscovery struct {
	r       *rand.Rand // 生成随机数
	mu      sync.RWMutex
	servers []string // 服务列表
	index   int      // 记录Robin算法的轮询到的位置
}

// NewMultiServerDiscovery 创建要一个MultiServerDiscovery实例
func NewMultiServerDiscovery(servers []string) *MultiServerDiscovery {
	d := &MultiServerDiscovery{
		servers: servers,
		r:       rand.New(rand.NewSource(time.Now().UnixNano())), // 初始化使用时间戳设定随机数种子
	}
	d.index = d.r.Intn(math.MaxInt32 - 1) // 生成要给整数的随机数
	return d
}

// 验证MultiServerDiscovery是否满足Discovery接口
var _ Discovery = (*MultiServerDiscovery)(nil)

// Refresh 从注册中心更新服务列表
func (d *MultiServerDiscovery) Refresh() error {
	return nil
}

// Update 手动更新服务列表
func (d *MultiServerDiscovery) Update(servers []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 重新设置服务列表
	d.servers = servers
	return nil
}

// Get 根据负载均衡策略，选择一个服务实例
func (d *MultiServerDiscovery) Get(mode SelectMode) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	n := len(d.servers) // 获取服务数量
	if n == 0 {
		return "", errors.New("rpc discovery: no available severs")
	}

	switch mode { // 选择不同的负载均衡策略
	case RandomSelect: // 随机选择
		return d.servers[d.r.Intn(n)], nil
	case RoundRobinSelect: // Robin算法
		s := d.servers[d.index%n]   // 从服务发现列表中，获取需要调度执行的服务
		d.index = (d.index + 1) % n // 通过Robin算法更新轮询位置
		return s, nil
	default:
		return "", errors.New("rpc discovery: not supported select mode")
	}
}

// GetAll 返回所有的服务实例
func (d *MultiServerDiscovery) GetAll() ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	servers := make([]string, len(d.servers), len(d.servers))
	// 复制一份服务发现列表，并返回
	copy(servers, d.servers)
	return servers, nil
}

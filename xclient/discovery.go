package xclient

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"
)

// SelectMode 代表不同的负载均衡策略，简单起见，GeeRPC 仅实现 Random 和 RoundRobin 两种策略。
type SelectMode int

const (
	RandomSelect     SelectMode = iota // 从服务列表中随机选择一个。
	RoundRobinSelect                   // 依次调度不同的服务器，每次调度执行 i = (i + 1) mode n。
)

// Discovery 是一个接口类型，包含了服务发现所需要的最基本的接口。
// Refresh() 从注册中心更新服务列表
// Update(servers []string)
// Get(mode SelectMode) 根据负载均衡策略，选择一个服务实例
// GetAll() 返回所有的服务实例
type Discovery interface {
	Refresh() error                      // 从注册中心更新服务列表
	Update(servers []string) error       // 手动更新服务列表
	Get(mode SelectMode) (string, error) // 根据负载均衡策略，选择一个服务实例
	GetAll() ([]string, error)           // 返回所有的服务实例
}

// MultiServersDiscovery 是一个不需要注册中心，服务列表由手工维护的服务发现的结构体
// 用户显式地提供服务器地址
type MultiServersDiscovery struct {
	r       *rand.Rand   // 产生随机数
	mu      sync.RWMutex // 保护
	servers []string     // 服务列表，保存着服务器的地址rpcAddr
	index   int          // 记录robin算法选择的位置(下一个选择)
}

func NewMultiServersDiscovery(servers []string) *MultiServersDiscovery {
	d := &MultiServersDiscovery{
		servers: servers,
		r:       rand.New(rand.NewSource(time.Now().UnixNano())), // r 是一个产生随机数的实例，初始化时使用时间戳设定随机数种子，避免每次产生相同的随机数序列。
	}
	// index 记录 Round Robin 算法已经轮询到的位置，为了避免每次从 0 开始，初始化时随机设定一个值。
	d.index = d.r.Intn(math.MaxInt32 - 1)
	return d
}

var _ Discovery = (NewMultiServersDiscovery)(nil)

// 从注册中心更新服务列表
func (d *MultiServersDiscovery) Refresh() error {
	return nil
}

// 手动更新服务列表
func (d *MultiServersDiscovery) Update(servers []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.servers = servers
	return nil
}

// 根据负载均衡策略，选择一个服务实例
func (d *MultiServersDiscovery) Get(mode SelectMode) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.servers)
	if n == 0 {
		return "", errors.New("rpc discovery: no available servers")
	}
	switch mode {
	case RandomSelect:
		return d.servers[d.r.Intn(n)], nil
	case RoundRobinSelect:
		s := d.servers[d.index%n]
		d.index = (d.index + 1) % n
		return s, nil
	default:
		return "", errors.New("rpc discovery: not supported select mode")
	}
}

// 返回所有的服务实例
func (d *MultiServersDiscovery) GetAll() ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// 返回 d.server的副本
	servers := make([]string, len(d.servers), len(d.servers))
	copy(servers, d.servers)
	return servers, nil
}

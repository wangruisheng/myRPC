package registry

import (
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// 首先定义 GeeRegistry 结构体，默认超时时间设置为 5 min，也就是说，任何注册的服务超过 5 min，即视为不可用状态。
type GeeRegistry struct {
	timeout time.Duration
	mu      sync.Mutex
	servers map[string]*ServerItem
}

type ServerItem struct {
	Addr  string
	start time.Time
}

const (
	defaultPath    = "/_geerpc_/registry"
	defaultTimeout = time.Minute * 5
)

// 新建一个 注册中心 实例并且有超时设定
func New(timeout time.Duration) *GeeRegistry {
	return &GeeRegistry{
		servers: make(map[string]*ServerItem),
		timeout: timeout,
	}
}

var DefaultGeeRegister = New(defaultTimeout)

// 添加服务实例，如果服务已经存在，则更新start
func (r *GeeRegistry) putServer(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.servers[addr]
	if s == nil {
		r.servers[addr] = &ServerItem{Addr: addr, start: time.Now()}
	}
}

// 返回可用的服务器列表，如果存在超时的服务，则删除
func (r *GeeRegistry) aliveServers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var alive []string
	for addr, s := range r.servers {
		if r.timeout == 0 || s.start.Add(r.timeout).After(time.Now()) {
			alive = append(alive, addr)
		} else {
			delete(r.servers, addr)
		}
	}
	sort.Strings(alive)
	return alive
}

// 为了实现上的简单，GeeRegistry 采用 HTTP 协议提供服务，且所有的有用信息都承载在 HTTP Header 中。
// ??? 为什么不用PRC，因为如果要用rpc，就得用conn传输，需要创建监听conn的方法，并且需要解析请求头当中的key，具体可以参考server的实现
// 实现http.Handler，在 /_geerpc_/registry 上运行
func (r *GeeRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case "GET": // 用来获取服务器实例
		// Get：返回所有可用的服务列表，通过自定义字段 X-Geerpc-Servers 承载。
		// 为了保持简单，直接把服务放在 请求头 中
		w.Header().Set("X-Geerpc-Servers", strings.Join(r.aliveServers(), ","))
	case "POST": // 用来添加服务实例
		// Post：添加服务实例或发送心跳，通过自定义字段 X-Geerpc-Server 承载。
		// 为了保持简单，直接把服务放在 请求头 中
		addr := req.Header.Get("X-Geerpc-Server")
		if addr == "" {
			// 设置响应行当中的状态码
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		r.putServer(addr)
	default:
		// 设置响应行当中的状态码
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *GeeRegistry) handleHTTP(registryPath string) {
	http.Handle(registryPath, r)
	log.Println("rpc registry path:", registryPath)
}

func HandleHTTP() {
	DefaultGeeRegister.handleHTTP(defaultPath)
}

// Hearbeat 提供 Heartbeat 方法，便于服务启动时定时向注册中心发送心跳，默认周期比注册中心设置的过期时间少 1 min。
// 它是服务器注册或发送心跳的辅助功能
func Hearbeat(registry, addr string, duration time.Duration) {
	if duration == 0 {
		// 确保在从注册表中删除之前有足够的时间发送心跳
		duration = defaultTimeout - time.Duration(1)*time.Minute
	}
	var err error
	err = sendHeartbeat(registry, addr)
	go func() {
		t := time.NewTicker(duration)
		for err == nil {
			<-t.C
			err = sendHeartbeat(registry, addr)
		}
	}()
}

func sendHeartbeat(registry, addr string) error {
	log.Println(addr, "send heart beat to registry", registry)
	httpClient := &http.Client{}
	// 向 registry 发送请求，将 X-Geerpc-Server 的 addr 注册到注册中心
	req, _ := http.NewRequest("POST", registry, nil)
	req.Header.Set("X-Geerpc-Server", addr)
	if _, err := httpClient.Do(req); err != nil {
		log.Println("rpc server: heart beat err:", err)
		return err
	}
	return nil
}

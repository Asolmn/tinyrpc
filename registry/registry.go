package registry

import (
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// TinyRegistry 注册中心
type TinyRegistry struct {
	timeout time.Duration // 超时时间
	mu      sync.Mutex
	servers map[string]*ServerItem // 服务端列表
}

type ServerItem struct {
	Addr  string    // 服务端地址
	start time.Time // 注册时间
}

const (
	defaultPath    = "/_tinyrpc_/registry" // 默认地址，注册中心采用HTTP协议
	defaultTimeout = time.Minute * 5       // 默认超时时间5min，任何注册的服务端超过5min即视为不可用状态
)

// New 初始化注册中心
func New(timeout time.Duration) *TinyRegistry {
	return &TinyRegistry{
		servers: make(map[string]*ServerItem),
		timeout: timeout,
	}
}

// 默认注册中心
var DefaultTinyRegister = New(defaultTimeout)

// putServer 添加服务端实例，如果服务端已经存在，则更新start
func (r *TinyRegistry) putServer(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 判断服务端是否已经在注册中心
	s, ok := r.servers[addr]
	if ok {
		s.start = time.Now() // 服务端存在，则更新注册时间
	} else {
		r.servers[addr] = &ServerItem{Addr: addr, start: time.Now()}
	}
}

// aliveServers 返回可用的服务列表，如果存在超时的服务，则删除
func (r *TinyRegistry) aliveServers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var alive []string
	for addr, s := range r.servers {
		// 进行服务端可用性判断
		// 条件为注册中心的超时时间为0 或 服务端注册时间加上超时时间之后不超过当前时间
		if r.timeout == 0 || s.start.Add(r.timeout).After(time.Now()) {
			alive = append(alive, addr)
		} else {
			delete(r.servers, addr)
		}
	}
	// 对可用服务端按递增顺序排序
	sort.Strings(alive)
	return alive
}

func (r *TinyRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.Method { // 根据请求中的GET或者POST方法，做出不同的处理
	case "GET": // 返回所有可用的服务列表，通过自定义字段X-Tinyrpc-Servers
		w.Header().Set("X-Tinyrpc-Servers", strings.Join(r.aliveServers(), ","))

	case "POST": // 添加服务实例或者发送心跳，通过自定义字段X-Tinyrpc-Server承载
		// 从Header中获取添加服务端的地址
		addr := req.Header.Get("X-Tinyrpc-Server")
		if addr == "" { // 如果地址为空，返回500报错
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// 添加服务端到注册中心
		r.putServer(addr)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (r *TinyRegistry) HandleHTTP(registryPath string) {
	// 请求处理, 路由为"/_tinyrpc_/registry"
	http.Handle(registryPath, r)
	// 日志输出rpc注册中心地址
	log.Println("rpc registry path:", registryPath)
}

func HandleHTTP() {
	// 调用默认注册中心的http处理请求方法
	DefaultTinyRegister.HandleHTTP(defaultPath)
}

// Heartbeat 定时向注册中心发送心跳，默认周期比注册中心设置的过期时间少1min
func Heartbeat(registry, addr string, duration time.Duration) {
	if duration == 0 { // 如果间隔时间为0
		// 发送心跳的间隔时间 = 默认超时时间 - 1分钟
		duration = defaultTimeout - time.Duration(1)*time.Minute
	}

	var err error
	// 发送心跳
	err = sendHeartbeat(registry, addr)
	go func() {
		t := time.NewTicker(duration) // 创建一个定时器，并设置间隔时间
		for err == nil {              // 进行无限循环发送心跳
			// 从定时器通道中读取一个时间到达事件，才再次调用发送心跳函数
			<-t.C
			err = sendHeartbeat(registry, addr)
		}
	}()

}

// sendHeartbeat 发送心跳
func sendHeartbeat(registry, addr string) error {
	log.Println(addr, "send heart beat to registry", registry)

	// 创建一个用于发送http请求的客户端
	httpClient := &http.Client{}

	// 创建一个post请求
	req, _ := http.NewRequest("POST", registry, nil)
	// 设置post的请求头中X-Tinyrpc-Server字段，用于承载服务端地址
	req.Header.Set("X-Tinyrpc-Server", addr)

	// 发送心跳请求
	if _, err := httpClient.Do(req); err != nil {
		log.Println("rpc server: heart beat err:", err)
		return err
	}

	return nil
}

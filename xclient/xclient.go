package xclient

import (
	"context"
	. "github.com/Asolmn/tinyrpc"
	"io"
	"reflect"
	"sync"
)

// XClient 支持负载均衡的客户端
type XClient struct {
	d    Discovery  // 服务发现实例
	mode SelectMode // 负载均衡模式
	opt  *Option    // 协议选项
	mu   sync.Mutex
	// 为例复用已经创建好的Socket连接，保存创建成功的Client实例
	clients map[string]*Client
}

// 检验XClient是否提供Close方法
var _ io.Closer = (*XClient)(nil)

// Close 关闭客户端
func (xc *XClient) Close() error {
	xc.mu.Lock()
	defer xc.mu.Unlock()

	// 循环获取Client实例，并进行关闭
	// 同时删除存放在clients列表中对应的Client实例
	for key, client := range xc.clients {
		_ = client.Close()
		delete(xc.clients, key)
	}
	return nil
}

// NewXClient 创建一个XClient实例
func NewXClient(d Discovery, mode SelectMode, opt *Option) *XClient {
	return &XClient{
		d:       d,
		mode:    mode,
		opt:     opt,
		clients: make(map[string]*Client),
	}
}

// dial 发起连接方法
func (xc *XClient) dial(rpcAddr string) (*Client, error) {
	xc.mu.Lock()
	defer xc.mu.Unlock()

	// 如果是已经存在的连接，从clients中获取
	client, ok := xc.clients[rpcAddr]
	if ok && !client.IsAvailable() { // 检查client是否可用状态
		_ = client.Close()
		delete(xc.clients, rpcAddr)
		client = nil
	}

	// 如果是新连接，通过XDial创建新的Client，并保存到clients中
	if client == nil {
		var err error
		client, err = XDial(rpcAddr, xc.opt) // 发起连接，获取Client实例
		if err != nil {
			return nil, err
		}
		xc.clients[rpcAddr] = client // 保存客户端
	}
	return client, nil
}

// 根据传入的地址，发起客户端连接，并进行Call操作
func (xc *XClient) call(rpcAddr string, ctx context.Context, serviceMethod string, args, reply interface{}) error {
	client, err := xc.dial(rpcAddr) // 发起连接
	if err != nil {
		return err
	}
	return client.Call(ctx, serviceMethod, args, reply)
}

// Call 对XClient的call操作的一层封装
// 调用call函数，等到完成，并返回其错误状态
func (xc *XClient) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	// 根据指定的负载策略，选择一个服务，并返回服务地址
	rpcAddr, err := xc.d.Get(xc.mode)
	if err != nil {
		return err
	}
	// 传入地址，进行Call操作
	return xc.call(rpcAddr, ctx, serviceMethod, args, reply)
}

// Broadcaset 请求广播到所有的服务实例
// 如果任意一个实例发生错误，则返回其中一个错误
// 如果调用成功，则返回其中一个结果
func (xc *XClient) Broadcaset(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	// 获取服务列表
	servers, err := xc.d.GetAll()
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var e error

	// 回复为nil，则无需设置值
	replyDone := reply == nil
	// 控制goroutine的停止，保证有错误发生时，快速失败
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 遍历服务列表
	for _, rpcAddr := range servers {
		wg.Add(1)
		// 对服务列表进行请求，使用并发操作
		go func(rpcAddr string) {
			defer wg.Done()

			// 克隆Reply变量
			var cloneReply interface{}

			// 如果reply不为空，则将reply的值转化为空接口类型，并复制给cloneReply
			if reply != nil {
				cloneReply = reflect.New(reflect.ValueOf(reply).Elem().Type()).Interface()
			}

			// 进行call操作，传入rpc地址，上下文，方法名，参数，返回参数
			err := xc.call(rpcAddr, ctx, serviceMethod, args, cloneReply)

			// 设置互斥锁，保证并发情况下error和reply能被正确赋值
			mu.Lock()
			if err != nil && e == nil { // call报错
				e = err
				cancel() // 通知使用了ctx的goroutine停止执行
			}
			// reply不为空时，将call返回的值，重新赋值给reply
			if err == nil && !replyDone {
				reflect.ValueOf(reply).Elem().Set(reflect.ValueOf(cloneReply).Elem())
				replyDone = true
			}
			mu.Unlock()
		}(rpcAddr)
	}
	wg.Wait()
	return e
}

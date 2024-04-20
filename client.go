package myRPC

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"myRPC/codec"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 【客户端-------接收响应、发送请求】
// 客户端支持异步和并发？？？

// 如何调用一个函数
// func (t *T) MethodName(argType T1, replyType *T2) error
// 根据上述要求，首先我们封装了结构体 Call 来承载一次 RPC 调用所需要的信息。

// Call 代表一个活跃的 RPC，封装一次 RPC 调用所需要的信息
type Call struct {
	Seq           uint64
	ServiceMethod string      // 形式"<service>.<method>"
	Args          interface{} // 函数的参数
	Reply         interface{} // 函数的返回值
	Error         error       // 如果错误发生，Error会被设置
	// 为了支持异步调用，Call 结构体中添加了一个字段 Done，Done 的类型是 chan *Call，当调用结束时，会调用 call.done() 通知调用方。
	Done chan *Call // 呼叫完成时挂起？？Strobes when call is complete.
}

// 当调用结束时，会调用 call.done() 通知调用方。
// 将完成的 call（里面包含在receive()方法时，已经从服务器返回并赋值的值）
func (call *Call) done() {
	call.Done <- call
}

// 客户端代表RPC客户端。
// 一个客户端可能有多个未完成的 Calls，一个客户端可能同时被多个 goroutines 使用。
type Client struct {
	cc      codec.Codec //cc 是消息的编解码器，和服务端类似，用来序列化将要发送出去的请求，以及反序列化接收到的响应。
	opt     *Option
	sending sync.Mutex   // sending 是一个互斥锁，和服务端类似，为了保证请求的有序发送，即防止出现多个请求报文混淆。
	header  codec.Header // 是每个请求的消息头，header 只有在请求发送时才需要，而请求发送是互斥的，因此每个客户端只需要一个，声明在 Client 结构体中可以复用。
	mu      sync.Mutex
	seq     uint64           // seq 用于给发送的请求编号，每个请求拥有唯一编号。
	pending map[uint64]*Call //pending 存储未处理完的请求，键是编号，值是 Call 实例。
	// 任意一个值置为 true，则表示 Client 处于不可用的状态，但有些许的差别，closing 是用户主动关闭的，即调用 Close 方法，而 shutdown 置为 true 一般是有错误发生。
	closing  bool
	shutdown bool
}

var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connection is shut down")

// Close 关闭连接
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing {
		return ErrShutdown
	}
	client.closing = true
	// 关闭cc里的通道
	return client.cc.Close()
}

// IsAvailable 如果客户端可以工作，则返回true
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return !client.shutdown && !client.closing
}

// registerCall call 添加到 client.pending 中，并更新 client.seq。
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}
	call.Seq = client.seq
	// 将要发送的 Call（请求）加入pending当中
	client.pending[call.Seq] = call
	// 请求序列号 +1
	client.seq++
	return call.Seq, nil
}

// removeCall 根据 seq，从 client.pending 中移除对应的 call，并返回。
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

// terminateCalls 服务端或客户端发生错误时调用，将 shutdown 设置为 true，且将错误信息通知所有 pending 状态的 call。
func (client *Client) terminateCalls(err error) {
	client.sending.Lock()
	defer client.sending.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	client.shutdown = true
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
}

// 接收响应-----接收到的响应有三种情况
// call 不存在，可能是请求没有发送完整，或者因为其他原因被取消，但是服务端仍旧处理了。
// call 存在，但服务端处理出错，即 h.Error 不为空。
// call 存在，服务端处理正常，那么需要从 body 中读取 Reply 的值。
func (client *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header
		// 在这里如果服务器没有响应应该会
		if err = client.cc.ReadHeader(&h); err != nil {
			break
		}
		call := client.removeCall(h.Seq)
		switch {
		case call == nil:
			// ？？？这通常意味着Write部分失败，调用已经被移除。如果写入报错也会在 send(）的时候直接移除
			err = client.cc.ReadBody(nil)
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done()
		default:
			err = client.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body" + err.Error())
			}
			call.done()
		}
	}
	// 错误发生了，因此terminateccall()执行将Call挂起
	client.terminateCalls(err)
}

// 创建 Client 实例时，首先需要完成一开始的协议交换，即发送 Option 信息给服务端。
// 协商好消息的编解码方式之后，再创建一个子协程调用 receive() 接收响应。
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodeType]
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodeType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}
	// 将 Option 发送给服务端
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: options error: ", err)
		_ = conn.Close()
		return nil, err
	}
	return newClientCodec(f(conn), opt), nil
}

func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1, //seq 从 1 开始，0 意味着无效call（call不可用？）
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	// 创建一个子协程调用 receive() 接收响应。
	go client.receive()
	return client
}

type clientResult struct {
	client *Client
	err    error
}

type newClientFunc func(conn net.Conn, opt *Option) (client *Client, err error)

// dialTimeout 在这里实现了一个超时处理的外壳 dialTimeout，这个壳将 NewClient 作为入参，在 2 个地方添加了超时处理的机制。
// 1) 将 net.Dial 替换为 net.DialTimeout，如果连接创建超时，将返回错误。
// 2) 使用子协程执行 NewClient，执行完成后则通过信道 ch 发送结果，如果 time.After() 信道先接收到消息，则说明 NewClient 执行超时，返回错误。
func dialTimeout(f newClientFunc, network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout(network, address, opt.ConnecTimeout)
	if err != nil {
		return nil, err
	}
	// 如果客户端为nil，关闭连接
	// TODO:PR 这里源码有问题???
	defer func() {
		if client == nil {
			_ = conn.Close()
		}
	}()
	ch := make(chan clientResult)
	go func() {
		client, err := f(conn, opt)
		ch <- clientResult{client: client, err: err}
	}()
	// 如果没有连接时间限制
	if opt.ConnecTimeout == 0 {
		result := <-ch
		return result.client, result.err
	}
	select {
	case <-time.After(opt.ConnecTimeout): // 如果超时
		return nil, fmt.Errorf("rpc client: connect timeout: except within %s", opt.ConnecTimeout)
	case result := <-ch: // 如果未超时就返回
		return result.client, result.err
	}
}

// parseOptions 解析 Options
// 还需要实现 Dial 函数，便于用户传入服务端地址，创建 Client 实例。为了简化用户调用
// 通过 ...*Option 将 Option 实现为可变参数列表，表示可以接受零个或多个。
func parseOptions(opts ...*Option) (*Option, error) {
	// 如果 []*Option数组为空
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("Number of options is more than 1")
	}
	// 如果opts[0]有值，变为我们要的值
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodeType == "" {
		opt.CodeType = DefaultOption.CodeType
	}
	return opt, nil
}

// Dial 通过指定的网络地址，连接到一个 RPC 服务
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	//opt, err := parseOptions(opts...)
	//if err != nil {
	//	return nil, err
	//}
	//conn, err := net.Dial(network, address)
	//if err != nil {
	//	return nil, err
	//}
	//// 如果客户端为 nil，关闭连接
	//defer func() {
	//	if client == nil {
	//		_ = conn.Close()
	//	}
	//}()
	//return NewClient(conn, opt)
	return dialTimeout(NewClient, network, address, opts...)
}

// send 实现发送请求的功能
func (client *Client) send(call *Call) {
	// 保证客户端会发送一个完整的请求
	client.sending.Lock()
	defer client.sending.Unlock()

	// 注册当前 call（请求）
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		// ？？为什么不removeCall()，是有可能注册没成功吗？
		return
	}

	// 准备请求头 header
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""

	// 编码并发送请求
	// 这里调用
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		// 用序列号重新取出call，把之前注册成功的 call 删掉
		call := client.removeCall(seq)
		// call 有可能是 nil，这种情况可能是写部分失败了（即registerCall部分写入没成功）
		// 客户端已经接收到响应并且处理
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

// Go 异步调用 send 函数
// 它返回表示调用的 Call 结构体
func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("rpc client: done channel is unbuffered")
	}
	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	client.send(call)
	return call
}

// Go 和 Call 是客户端暴露给用户的两个 RPC 服务调用接口，Go 是一个异步接口，返回 call 实例。
// 是对 Go 方法的封装，阻塞call.Done，等待响应返回，是一个同步接口
// Client.Call 的超时处理机制，使用 context 包实现，控制权交给用户，控制更为灵活。
func (client *Client) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	// 当前 Call 完成才可以进行下一个
	// call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	call := client.Go(serviceMethod, args, reply, make(chan *Call, 1))
	select {
	// 用户可以使用 context.WithTimeout 创建具备超时检测能力的 context 对象来控制
	case <-ctx.Done():
		client.removeCall(call.Seq)
		return errors.New("rpc client: call failed: " + ctx.Err().Error())
	case call := <-call.Done:
		return call.Error
	}
	return call.Error
}

// NewHTTPClient 创建一个新的客户端实例通过HTTP，来传输协议
func NewHTTPClient(conn net.Conn, opt *Option) (*Client, error) {
	// 通过连接 conn 向服务器发送一个 HTTP 请求
	// 请求头格式 Method Request-URI HTTP-Version
	_, _ = io.WriteString(conn, fmt.Sprintf("CONNECT %s HTTP/1.0\n\n", defaultRPCPath))

	// 在转换到 RPC 协议之前，获取成功的 HTTP 请求
	// 这一行代码调用了 http.ReadResponse() 函数，从连接中读取 HTTP 响应并解析它。它传递了一个 bufio.NewReader(conn) 作为读取器，用于从连接中读取响应数据，同时传递了一个新创建的 http.Request 对象作为预期的请求属性。这个请求对象指定了请求方法为 CONNECT。
	// 解析完响应后，将响应对象赋值给 resp 变量，并将可能出现的错误赋值给 err 变量。
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err == nil && resp.Status == connected {
		return NewClient(conn, opt)
	}
	if err == nil {
		err = errors.New("unexpected HTTP response: " + resp.Status)
	}
	return nil, err
}

// DialHTTP 通过特定的网络地址连接到一个 HTTP RPC 服务
// 监听默认的 HTTP RPC 路径
func DialHTTP(network, address string, opts ...*Option) (*Client, error) {
	return dialTimeout(NewHTTPClient, network, address, opts...)
}

// 根据第一个参数rpcAddr, XDial调用不同的函数来连接到RPC服务。
// rpcAddr是表示rpc服务器的通用格式(protocol@addr)，
// 例如http@10.0.0.1:7001, tcp@10.0.0.1:9999,unix@/tmp/geerpc.sock
func XDial(rpcAddr string, opts ...*Option) (*Client, error) {
	parts := strings.Split(rpcAddr, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("rpc client err: wrong format '%s', expect protocol@addr", rpcAddr)
	}
	protocal, addr := parts[0], parts[1]
	switch protocal {
	case "http":
		return DialHTTP("tcp", addr, opts...)
	default:
		// tcp，unix 或者其他交换协议
		return Dial(protocal, addr, opts...)
	}
}

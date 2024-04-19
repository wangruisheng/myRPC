package myRPC

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"myRPC/codec"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"
)

// 服务端的实现阶段

// 【服务端-------接收请求、发送响应】

const MagicNumber = 0x3bef5c

// Option GeeRPC 客户端固定采用 JSON 编码 Option，
// 后续的 header 和 body 的编码方式由 Option 中的 CodeType 指定，
// 服务端首先使用 JSON 解码 Option，然后通过 Option 的 CodeType 解码剩余的内容。
type Option struct {
	MagicNumber   int           // MagicNumber marks this's a geerpc request，表示这是 geerpc 的请求
	CodeType      codec.Type    // client may choose different Codec to encode body，body编码格式
	ConnecTimeout time.Duration // 0 意为着没有限制
	HandleTimeout time.Duration // 处理报文时长限制
}

var DefaultOption = &Option{
	MagicNumber:   MagicNumber,
	CodeType:      codec.GobType,
	ConnecTimeout: time.Second * 10,
}

// Server represents an RPC Server.
type Server struct {
	// Map类似于Go的Map [interface{}]interface{}，但是对于多个例程并发使用是安全的，不需要额外的锁或协程
	serviceMap sync.Map
}

// Register 在 服务器Server 中注册对应结构体的方法
func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	// LoadOrStore返回键的现有值(如果存在)。否则，它存储并返回给定的值。如果值已加载，则加载结果为true，如果已存储，则加载结果为false。
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service alredy defined: " + s.name)
	}
	return nil
}

// Register 在默认服务当中注册方法
func Register(rcvr interface{}) error {
	return DefaultServer.Register(rcvr)
}

// 通过 ServiceMethod 从 serviceMap 中找到对应的 service
func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMethod, ".")
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed" + serviceMethod)
		return
	}
	//  ServiceMethod 的构成是 “Service.Method”，因此先将其分割成 2 部分，第一部分是 Service 的名称
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	// serviceMap 中找到对应的 service 实例
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service " + serviceName)
		return
	}
	svc = svci.(*service)
	// 再从 service 实例的 method 中，找到对应的 methodType。
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method" + methodName)
	}
	return

}

// NewServer returns a new Server.
func NewServer() *Server {
	return &Server{}
}

// DefaultServer is the default instance of *Server.
var DefaultServer = NewServer()

// Accept 实现了 Accept 方式，net.Listener 作为参数
func (server *Server) Accept(lis net.Listener) {
	for {
		// for 循环等待 socket 连接建立
		conn, err := lis.Accept()
		if err != nil {
			log.Println("rpc server: accept err:", err)
			return
		}

		// 开启子协程处理，处理过程交给了 ServerConn 方法。
		go server.ServeConn(conn)
	}
}

// Acccept 直接默认用 DefaultServer 作为服务器，所以可以不用自己创建实例server，再用server.Accpet()
// Accept accepts connections on the listener and serves requests
// for each incoming connection.
func Acccept(lis net.Listener) {
	// DefaultServer 是一个默认的 Server 实例，主要为了用户使用方便。
	DefaultServer.Accept(lis)
}

// ServeConn 的实现就和之前讨论的通信过程紧密相关了，
// 首先使用 json.NewDecoder 反序列化得到 Option 实例，检查 MagicNumber 和 CodeType 的值是否正确。
// 然后根据 CodeType 得到对应的消息编解码器，
// 接下来的处理交给 serverCodec。
// 报文将以这样的形式发送：
// | Option{MagicNumber: xxx, CodecType: xxx} | Header{ServiceMethod ...} | Body interface{} |
// | <------      固定 JSON 编码      ------>  | <-------   编码方式由 CodeType 决定   ------->
// 在一次连接中，Option 固定在报文的最开始，Header 和 Body 可以有多个，即报文可能是这样的。
// | Option | Header1 | Body1 | Header2 | Body2 | ...
func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	defer func() { _ = conn.Close() }()
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error", err)
		return
	}
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server: invalid magic number %x", opt.MagicNumber)
		return
	}
	// 获得对应 CodeType 的处理函数
	f := codec.NewCodecFuncMap[opt.CodeType]
	if f == nil {
		log.Printf("rpc server: invalid codec type %s", opt.CodeType)
		return
	}
	server.serveCodec(f(conn), &opt)

}

// invalidRequest是发生错误时响应argv的占位符
var invalidRequest = struct{}{}

// serveCodec 进行 Header 和 Body 部分的解码
// serveCodec 的过程非常简单。主要包含三个阶段
// 读取请求 readRequest
// 处理请求 handleRequest
// 回复请求 sendResponse
// 之前提到过，在一次连接中，允许接收多个请求，即多个 request header 和 request body，
// 因此这里使用了 for 无限制地等待请求的到来，直到发生错误（例如连接被关闭，接收到的报文有问题等）
// 这里需要注意的点有三个：
// handleRequest 使用了协程并发执行请求。
// 处理请求是并发的，但是回复请求的报文必须是逐个发送的，并发容易导致多个回复报文交织在一起，客户端无法解析。在这里使用锁(sending)保证。
// 尽力而为，只有在 header 解析失败时，才终止循环。
func (server *Server) serveCodec(cc codec.Codec, opt *Option) {
	sending := new(sync.Mutex) // 互斥锁，保证发送一个完整的响应
	wg := new(sync.WaitGroup)  // 等待，直到所有的请求都得到处理
	for {
		// 处理请求是并发的，不用锁
		req, err := server.readRequest(cc)
		if err != nil {
			if req == nil {
				break // 没有办法恢复，所以关闭连接
			}
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		go server.handleRequest(cc, req, sending, wg, opt.HandleTimeout)
	}
	// 当主 goroutine 执行 Wait() 方法时，如果计数器的值不为零，它就会被阻塞，
	// 直到所有子 goroutine 执行 Done() 方法使计数器减为零，才会继续执行。
	wg.Wait()
	_ = cc.Close()
}

// request 当中储存一次访问的所有信息
type request struct {
	h            *codec.Header
	argv, replyv reflect.Value
	mtype        *methodType
	svc          *service
}

func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	// 对 header 进行解码
	if err := cc.ReadHeader(&h); err != nil {
		// 如果不是达到文件结束（End of File EOF）
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error:", err)
		}
		return nil, err
	}
	return &h, nil
}

func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}
	req := &request{h: h}
	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()
	// req.argv.Type().Kind() != reflect.Ptr 来检查请求参数的类型是否为指针类型。
	//如果不是指针类型，就通过 req.argv.Addr() 获取其指针，并再次调用 Interface() 方法获取指针的接口值【转为interfance{}类型】。
	//这样做的目的是为了确保最终获取到的 argvi 是请求参数的指针类型的接口值。
	// 因为argvi是指针类型，和req.argv指向同一个内存地址，所以cc.ReadBody(argvi)读到的值，最终会在req.argv里
	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr {
		argvi = req.argv.Addr().Interface()
	}
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read body err:", err)
		return req, err
	}
	return req, nil
}

func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error:", err)
	}
}

// 通过 req.svc.call 完成方法调用，将 replyv 传递给 sendResponse 完成序列化即可。
// 这里需要确保 sendResponse 仅调用一次，因此将整个过程拆分为 called 和 sent 两个阶段，在这段代码中只会发生如下两种情况：
// 1) called 信道接收到消息，代表处理没有超时，继续执行 sendResponse。
// 2) time.After() 先于 called 接收到消息，说明处理已经超时，called 和 sent 都将被阻塞。在 case <-time.After(timeout) 处调用 sendResponse。
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	// 应调用已注册的rpc方法，获得正确的replyv
	// day 1, 只是打印了 argv 并发送了 hello 信息
	// 调用 Done() ，计数器减一
	defer wg.Done()
	called := make(chan struct{})
	sent := make(chan struct{})
	go func() {
		err := req.svc.call(req.mtype, req.argv, req.replyv)
		called <- struct{}{}
		if err != nil {
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			sent <- struct{}{}
			return
		}
		server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
		sent <- struct{}{}
	}()
	// 如果不限制超时时间
	if timeout == 0 {
		<-called
		<-sent
		return
	}
	select {
	case <-time.After(timeout):
		req.h.Error = fmt.Sprintf("rpc server: request handle timeout: except within %s", timeout)
		server.sendResponse(cc, req.h, invalidRequest, sending)
	case <-called:
		<-sent
	}

}

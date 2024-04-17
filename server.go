package myRPC

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"myRPC/codec"
	"net"
	"reflect"
	"sync"
)

// 服务端的实现阶段

// 【服务端-------接收请求、发送响应】

const MagicNumber = 0x3bef5c

// GeeRPC 客户端固定采用 JSON 编码 Option，
// 后续的 header 和 body 的编码方式由 Option 中的 CodeType 指定，
// 服务端首先使用 JSON 解码 Option，然后通过 Option 的 CodeType 解码剩余的内容。
type Option struct {
	MagicNumber int        // MagicNumber marks this's a geerpc request，表示这是 geerpc 的请求
	CodeType    codec.Type // client may choose different Codec to encode body，body编码格式
}

var DefaultOption = &Option{
	MagicNumber: MagicNumber,
	CodeType:    codec.GobType,
}

// Server represents an RPC Server.
type Server struct {
}

// NewServer returns a new Server.
func NewServer() *Server {
	return &Server{}
}

// DefaultServer is the default instance of *Server.
var DefaultServer = NewServer()

// Acccept 实现了 Accept 方式，net.Listener 作为参数
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

// 直接默认用 DefaultServer 作为服务器，所以可以不用自己创建实例server，再用server.Accpet()
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
	server.serveCodec(f(conn))

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
func (server *Server) serveCodec(cc codec.Codec) {
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
		go server.handleRequest(cc, req, sending, wg)
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
	// TODO：现在我们不知道请求类型
	// day1: 目前还不能判断 body 的类型，因此在 readRequest 和 handleRequest 中，day1 将 body 作为字符串处理。
	//接收到请求，打印 header，并回复 geerpc resp ${req.h.Seq}。这一部分后续再实现。
	req.argv = reflect.New(reflect.TypeOf(""))
	if err = cc.ReadBody(req.argv.Interface()); err != nil {
		log.Println("rpc server: read argv err:", err)
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

func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	// TODO 应调用已注册的rpc方法，获得正确的replyv
	// day 1, 只是打印了 argv 并发送了 hello 信息
	// 调用 Done() ，计数器减一
	defer wg.Done()
	log.Println(req.h, req.argv.Elem())
	req.replyv = reflect.ValueOf(fmt.Sprintf("geerpc resp %d", req.h.Seq))
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
}

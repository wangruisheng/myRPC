package main

import (
	"context"
	"log"
	"myRPC"
	"net"
	"net/http"
	"sync"
	"time"
)

// 实现一个简易的客户端
// 在 startServer 中使用了信道 addr，确保服务端端口监听成功，客户端再发起请求。
// 客户端首先发送 Option 进行协议交换，接下来发送消息头 h := &codec.Header{}，和消息体 geerpc req ${h.Seq}。
// 最后解析服务端的响应 reply，并打印出来。

// Foo 定义结构体 Foo 和方法 Sum
type Foo int

type Args struct {
	Num1, Num2 int
}

func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func main() {
	// 版本1：客户端服务器测试
	//addr := make(chan string)
	//go startServer(addr)
	//
	//// 下面的代码相当于一个简单的客户端
	//conn, _ := net.Dial("tcp", <-addr)
	//defer func() {
	//	_ = conn.Close()
	//}()
	//
	//time.Sleep(time.Second)
	//// 发送操作，向conn中发送编码好的数据
	//_ = json.NewEncoder(conn).Encode(myRPC.DefaultOption)
	//cc := codec.NewGobCodec(conn)
	//// 发送请求，接受响应
	//for i := 0; i < 5; i++ {
	//	h := &codec.Header{
	//		ServiceMethod: "Foo.Sum",
	//		Seq:           uint64(i),
	//	}
	//	// 向 conn 中写入请求
	//	_ = cc.Write(h, fmt.Sprintf("geerpc req %d", h.Seq))
	//	_ = cc.ReadHeader(h)
	//	var reply string
	//	_ = cc.ReadBody(&reply)
	//	log.Println("reply:", reply)
	//}

	// 版本2
	// 在 main 函数中使用了 client.Call 并发了 5 个 RPC 同步调用，参数和返回值的类型均为 string。
	//log.SetFlags(0)
	//addr := make(chan string)
	//go startServer(addr)
	//// 这个client就会调用 recive() 不断监听接受响应，直到报错
	//client, _ := myRPC.Dial("tcp", <-addr)
	//defer func() { _ = client.Close() }()
	//
	//time.Sleep(time.Second)
	//// 发送请求，接受响应
	//var wg sync.WaitGroup
	//for i := 0; i < 5; i++ {
	//	wg.Add(i)
	//	go func(i int) {
	//		defer wg.Done()
	//		args := fmt.Sprintf("myrpc req %d", i)
	//		var reply string
	//		if err := client.Call("Foo.Sum", args, &reply); err != nil {
	//			log.Fatal("call Foo.Sum error:", err)
	//		}
	//		log.Println("reply:", reply)
	//	}(i)
	//}
	//wg.Wait()

	// 版本3
	// 构造参数，发送 RPC 请求，并打印结果。
	//log.SetFlags(0)
	//addr := make(chan string)
	//go startServer(addr)
	//client, _ := myRPC.Dial("tcp", <-addr)
	//defer func() {
	//	_ = client.Close()
	//}()
	//time.Sleep(time.Second)
	//// 发送请求并接收响应
	//var wg sync.WaitGroup
	//for i := 0; i < 5; i++ {
	//	wg.Add(1)
	//	go func(i int) {
	//		defer wg.Done()
	//		args := Args{
	//			Num1: i,
	//			Num2: i * i,
	//		}
	//		var reply int
	//		if err := client.Call(context.Background(), "Foo.Sum", args, &reply); err != nil {
	//			log.Fatal("call Foo.Sum error:", err)
	//		}
	//		log.Printf("%d + %d = %d", args.Num1, args.Num2, reply)
	//	}(i)
	//}
	//// 到0才继续执行
	//wg.Wait()

	// 版本4
	// 客户端将 Dial 替换为 DialHTTP，其余地方没有发生改变。
	// 并且客户端单独用协程运行
	log.SetFlags(0)
	ch := make(chan string)
	go call(ch)
	startServer(ch)
}

func call(addr chan string) {
	client, _ := myRPC.DialHTTP("tcp", <-addr)
	defer func() {
		_ = client.Close()
	}()
	time.Sleep(time.Second)
	// 发送请求并接收响应
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := Args{
				Num1: i,
				Num2: i * i,
			}
			var reply int
			if err := client.Call(context.Background(), "Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Printf("%d + %d = %d", args.Num1, args.Num2, reply)
		}(i)
	}
	// 到0才继续执行
	wg.Wait()
}

// 启动服务器，并让服务器选择一个 ip端口 进行监听
func startServer(addr chan string) {
	// 选择一个空闲的 ip+端口 进行监听
	//l, err := net.Listen("tcp", ":0")
	//if err != nil {
	//	log.Fatal("network error:", err)
	//}
	//log.Println("start rpc server on", l.Addr())
	//addr <- l.Addr().String()
	//myRPC.Acccept(l)

	// 版本3
	// 注册 Foo 到 Server 中，并启动 RPC 服务
	//var foo Foo
	//if err := myRPC.Register(&foo); err != nil {
	//	log.Fatal("register error :", err)
	//}
	//// 选择空闲的结点
	//l, err := net.Listen("tcp", ":0")
	//if err != nil {
	//	log.Fatal("network error:", err)
	//}
	//log.Println("start rpc server on", l.Addr())
	//addr <- l.Addr().String()
	//myRPC.Accept(l)

	// 版本4
	// 将 startServer 中的 geerpc.Accept() 替换为了 geerpc.HandleHTTP()，端口固定为 9999。
	var foo Foo
	_ = myRPC.Register(&foo)
	// 选择端口9999
	l, _ := net.Listen("tcp", ":9999")
	// 注册 HTTP handler
	myRPC.HandleHTTP()
	addr <- l.Addr().String()
	_ = http.Serve(l, nil)
}

package myRPC

import (
	"go/ast"
	"log"
	"reflect"
	"sync/atomic"
)

// 每一个 methodType 实例包含了一个方法的完整信息。包括
//
// method：方法本身
// ArgType：第一个参数的类型
// ReplyType：第二个参数的类型
// numCalls：后续统计方法调用次数时会用到
type methodType struct {
	method    reflect.Method
	ArgType   reflect.Type
	ReplyType reflect.Type
	numCalls  uint64
}

func (m *methodType) NumCalls() uint64 {
	return atomic.LoadUint64(&m.numCalls)
}

// 这块有点看不懂
// newArgv 和 newReplyv，用于创建对应类型的实例
func (m *methodType) newArgv() reflect.Value {
	var argv reflect.Value
	// arg 可能是指针类型，或者value类型
	// 指针类型和值类型创建实例的方式有细微区别
	if m.ArgType.Kind() == reflect.Ptr {
		// 返回一个对应类型的0值，类型为Value
		// 如果是指针类型，m.ArgType.Elem()获得这个指针指向的实际类型，
		// reflect.New(m.ArgType.Elem())获得指向m.ArgType.Elem()这个类型的零值的指针，指针类型是reflect.Value
		// 【是指针类型，最后返回的也是指针类型】
		argv = reflect.New(m.ArgType.Elem())
	} else {
		// 如果不是指针类型，m.ArgType直接就是实际类型
		// reflect.New(m.ArgType)获得指向m.ArgType这个类型的零值的指针，指针类型是reflect.Value
		// reflect.New(m.ArgType).Elem()获得这个指针类型的实际类型，并赋给argv
		// 【是值类型，最终返回的也是值类型】
		argv = reflect.New(m.ArgType).Elem()
	}
	return argv
}

func (m *methodType) newReplyv() reflect.Value {
	// reply 一定是指针类型
	// New返回一个Value，表示指向指定类型的新零值的指针。也就是说，返回值的类型是PointerTo(Type)。
	replyv := reflect.New(m.ReplyType.Elem())
	switch m.ReplyType.Elem().Kind() {
	// ？？ replyType一定是这几种数据结构吗，不一定，如果是的话则会创建 Map 或 Slice
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(m.ReplyType.Elem()))
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0))
	}
	return replyv
}

type service struct {
	name   string                 // 映射的结构体的名称
	typ    reflect.Type           // 结构体的类型
	rcvr   reflect.Value          // 结构体的实例本身
	method map[string]*methodType // 存储映射的结构体的所有符合条件的方法
}

// newService 构造函数
func newService(rcvr interface{}) *service {
	s := new(service)
	s.rcvr = reflect.ValueOf(rcvr)
	// 如果 s.rcvr是指针，Indirect()返回s.rcvr指向的值，如果本来s.rcvr就是值，那么直接返回原值
	s.name = reflect.Indirect(s.rcvr).Type().Name()
	s.typ = reflect.TypeOf(rcvr)
	if !ast.IsExported(s.name) {
		log.Fatalf("rpc server: %s is not a valid service name", s.name)
	}
	// log.Printf("add : %p", &s)
	s.registerMethods()
	// log.Printf("add : %p", &s)
	return s
}

// registerMethods 过滤出了符合条件的方法：
//
//	两个导出或内置类型的入参（反射时为 3 个，第 0 个是自身，类似于 python 的 self，java 中的 this）
//	返回值有且只有 1 个，类型为 error
func (s *service) registerMethods() {
	// log.Printf("add : %p", &s)
	s.method = make(map[string]*methodType)
	for i := 0; i < s.typ.NumMethod(); i++ {
		method := s.typ.Method(i)
		mType := method.Type
		// 入参和出参的个数
		if mType.NumIn() != 3 || mType.NumOut() != 1 {
			continue
		}
		// 如果返回值的类型不为 error
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}
		// 得到该方法的 argType（参数类型），replyType（响应类型）
		argType, replyType := mType.In(1), mType.In(2)
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}
		s.method[method.Name] = &methodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
		}
		log.Printf("rpc server: register %s.%s\n", s.name, method.Name)
	}
	// log.Printf("add : %p", &s)
}

func isExportedOrBuiltinType(t reflect.Type) bool {
	// PkgPath返回已定义类型的包路径，即唯一标识包的导入路径
	// IsExported报告名称是否以大写字母开头。
	return ast.IsExported(t.Name()) || t.PkgPath() == ""
}

func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	atomic.AddUint64(&m.numCalls, 1)
	f := m.method.Func
	// 调用带有参数的函数 f
	returnValues := f.Call([]reflect.Value{s.rcvr, argv, replyv})
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}
	return nil
}

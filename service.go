package tinyrpc

import (
	"go/ast"
	"log"
	"reflect"
	"sync/atomic"
)

// methodType 包含一个方法的完整信息
type methodType struct {
	method    reflect.Method // 方法本身
	ArgType   reflect.Type   // 第一个参数的类型
	ReplyType reflect.Type   // 第二个参数的类型
	numCalls  uint64         // 用于后续统计方法调用次数
}

func (m *methodType) NumCalls() uint64 {
	// atomic.LoadUint64 用于原子性读取一个uint64类型变量的值
	return atomic.LoadUint64(&m.numCalls)
}

// newArgv 用于创建对应类型的实例
func (m *methodType) newArgv() reflect.Value {
	var argv reflect.Value

	/*
		reflect.Value.Elem() Elem返回v持有的接口保管的值的Value封装
		或者v持有的指针指向的值的Value封装。如果v的Kind不是Interface或Ptr会panic；
		如果v持有的值为nil，会返回Value零值。
		reflect.Type.Elem() 返回该类型的元素类型
		如果该类型的Kind不是Array、Chan、Map、Ptr或Slice，会panic

		判断第一个参数的类型是否为reflect.Ptr类型，即指针类型

		如果是指针类型，则使用reflect.New()创建一个指向m.ArgType具体类型的零值的指针
		并将其封装为一个reflect.

		如果不是指针类型，则使用reflect.New()创建一个指向m.ArgType类型的零值的指针
		并使用Elem获取其指向的值，并封装为一个reflect.Value类型的值
	*/
	if m.ArgType.Kind() == reflect.Ptr {
		argv = reflect.New(m.ArgType.Elem())
	} else {
		argv = reflect.New(m.ArgType).Elem()
	}
	return argv
}

// newReplyv 用于创建对应类型的实例
func (m *methodType) newReplyv() reflect.Value {
	// 创建一个指向m.ReplyType类型的零值的指针
	replyv := reflect.New(m.ReplyType.Elem())

	/*
		判断ReplyType的类型是否为Slice和Map类型
		如果ReplyType为Map类型，则创建一个指定为m.ReplyType类型的映射，并封装成reflect.Value类型
		使用Set将replyv的反射值为更新为刚刚创建的映射的反射值
		如果ReplyType为Slice类型，与上同理
	*/
	switch m.ReplyType.Elem().Kind() {
	case reflect.Map:
		replyv.Elem().Set(reflect.MakeMap(m.ReplyType.Elem()))
	case reflect.Slice:
		replyv.Elem().Set(reflect.MakeSlice(m.ReplyType.Elem(), 0, 0))
	}

	return replyv
}

// service 包含一个service的全部信息
type service struct {
	name   string                 // 映射的结构体的名称
	typ    reflect.Type           // 结构体的类型
	rcvr   reflect.Value          // 结构体的实例本身，保留rcvr是因为在调用时需要rcvr作为第0个参数
	method map[string]*methodType // 存储映射的结构体的所有符合条件的方法
}

// newService 构造函数，参数为任意需要映射为服务的结构体实例
func newService(rcvr interface{}) *service {

	s := new(service)              // 创建一个service实例
	s.rcvr = reflect.ValueOf(rcvr) // 将rcvr封装为reflect.Value类型

	// Indirect 返回持有v持有的指针指向的值的Value。如果v持有nil指针，会返回Value零值；如果v不持有指针，会返回v。
	s.name = reflect.Indirect(s.rcvr).Type().Name() // 获取s.rcvr的类型名字
	//fmt.Println(s.name)
	s.typ = reflect.TypeOf(rcvr) // 将s.rcvr的类型转换为reflect.Type类型

	// 判断所映射的结构体是否可以导出，如果不可以导出，则进行报错
	if !ast.IsExported(s.name) {
		log.Fatalf("rpc server: %s is not a valid service name", s.name)
	}

	s.registerMethods() // 注册所有结构体的所有方法

	return s // 返回service实例
}

// registerMethods 注册方法到服务
func (s *service) registerMethods() {
	// 初始化method
	s.method = make(map[string]*methodType)

	// 遍历typ的方法
	for i := 0; i < s.typ.NumMethod(); i++ {

		method := s.typ.Method(i) // 获取typ中的第i个方法
		mType := method.Type      // 获取method的方法类型

		// 如果method方法的参数不等于3，且返回个数不等于1，则跳过当前method
		if mType.NumIn() != 3 || mType.NumOut() != 1 {
			continue
		}
		// 如果method方法的第0个返回值，不等于error类型，则跳过当前method
		if mType.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
			continue
		}

		// 获取method第一个和第二个参数，分别赋予argType和replyType
		argType, replyType := mType.In(1), mType.In(2)

		// 如果argType和replyType不可以导出或者不为内建类型，则跳过当前method
		if !isExportedOrBuiltinType(argType) || !isExportedOrBuiltinType(replyType) {
			continue
		}

		// 存储符合条件的方法，以方法名为键，methodType实例作为值
		s.method[method.Name] = &methodType{
			method:    method,
			ArgType:   argType,
			ReplyType: replyType,
		}
		// 输出rpc服务注册信息
		log.Printf("rpc server: register %s.%s\n", s.name, method.Name)
	}
}

// isExportedOrBuiltinType 校验方法条件，是否可以导出和为内建类型
func isExportedOrBuiltinType(t reflect.Type) bool {
	// PkgPath返回类型的包路径，即明确指定包的import路径，如"encoding/base64"
	// 如果类型为内建类型(string, error)或未命名类型(*T, struct{}, []int)，会返回""

	// 判断t是否可以导出或者为内建类型和未命名类型
	return ast.IsExported(t.Name()) || t.PkgPath() == ""
}

// call 实现通过反射值调用方法
func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	// 调用次数+1
	atomic.AddUint64(&m.numCalls, 1)

	// 获取方法的值
	f := m.method.Func

	// 因为method.Func返回的为方法的值，是一种方法变量
	// Call的执行调用，相当于一种方法表达式，所有s.rcvr作为方法的接受者，则成为函数的第一个形参
	// 等价于调用s.rcvr.method(argv, replyv)
	// 最后方法的返回结果为reflect.Value封装的Slice
	returnValues := f.Call([]reflect.Value{s.rcvr, argv, replyv})

	// 获取方法返回的错误信息
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}

	return nil
}

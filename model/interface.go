package model

import (
	protomessage "infra-foundation/protomessage"
	"infra-foundation/session"
	"net/http"
	"sync"

	"google.golang.org/protobuf/proto"
)

func Register(m Model) error {
	return DefaultModelManager.Register(m)
}

type handler struct {
	model  *modelActor
	name   string
	pbPool sync.Pool
	// handle 接收的是已经从 pool 中取出的对象，调用方负责回收。
	handle func(*session.Context, protomessage.ProtoMessage)
}

func (h *handler) Put(pb protomessage.ProtoMessage) {
	if pb == nil {
		return
	}
	proto.Reset(pb)
	h.pbPool.Put(pb)
}

var (
	handlersMu sync.RWMutex
	handlers   = map[int32]*handler{}
)

func IsLocalHandler(id int32) bool {
	handlersMu.RLock()
	defer handlersMu.RUnlock()
	_, ok := handlers[id]
	return ok
}

func HandlersRoutes() []int32 {
	handlersMu.RLock()
	defer handlersMu.RUnlock()
	routes := make([]int32, 0, len(handlers))
	for id := range handlers {
		routes = append(routes, id)
	}
	return routes
}

// RegisterMsgHandler 注册一个基于 handler 函数的回调。
// 这是旧版兼容接口；新代码建议直接使用 RegisterTypedHandler 泛型注册，以获得零反射的性能。
func RegisterMsgHandler(pb protomessage.ProtoMessage, fn func(*session.Context, protomessage.ProtoMessage)) {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	hd := &handler{name: pb.ModeName(), handle: fn}
	hd.pbPool = sync.Pool{New: func() any { return proto.Clone(pb) }}
	handlers[pb.MessageID()] = hd
}

// RegisterTypedHandler 以泛型方式注册 handler，避免业务层使用反射包装方法。
// 示例：
//
//	model.RegisterTypedHandler(&MyProto{}, func(ctx *session.Context, pb *MyProto) { ... })
func RegisterTypedHandler[T protomessage.ProtoMessage](pb T, fn func(*session.Context, T)) {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	hd := &handler{name: pb.ModeName(), handle: func(ctx *session.Context, msg protomessage.ProtoMessage) {
		fn(ctx, msg.(T))
	}}
	hd.pbPool = sync.Pool{New: func() any {
		return proto.Clone(pb)
	}}
	handlers[pb.MessageID()] = hd
}

var httpHandlers sync.Map

type httpHandlr func(w http.ResponseWriter, r *http.Request)

func RegisterHttpHandler(path string, cb httpHandlr) {
	httpHandlers.Store(path, cb)
}

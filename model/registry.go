package model

import (
	"infra-foundation/message"
	"infra-foundation/session"
	"net/http"
	"sync"

	"google.golang.org/protobuf/proto"
)

func Register(m Model) error {
	return DefaultModelManager.Register(m)
}

type protoHandler struct {
	name     string
	newProto func() message.Message
	handle   func(*session.Context, message.Message)
}

func IsLocalHandler(id int32) bool {
	return DefaultModelManager.IsLocalHandler(id)
}

func GetLocalHandlerIDs() []int32 {
	return DefaultModelManager.GetLocalHandlerIDs()
}

func RegisterTypedHandler[T message.Message](pb T, fn func(*session.Context, T)) {
	DefaultModelManager.RegisterHandler(pb.MessageID(), pb.ModelName(), func() message.Message {
		return proto.Clone(pb).(message.Message)
	}, func(ctx *session.Context, msg message.Message) {
		fn(ctx, msg.(T))
	})
}

var httpHandlers sync.Map

type httpHandler func(w http.ResponseWriter, r *http.Request)

func RegisterHTTPHandler(path string, cb httpHandler) {
	httpHandlers.Store(path, cb)
}

package message

import "google.golang.org/protobuf/proto"

type Message interface {
	proto.Message
	MessageID() int32
	MessageName() string
	ServiceName() string
	ModelName() string
}

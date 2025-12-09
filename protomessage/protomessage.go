package protomessage

import "google.golang.org/protobuf/proto"

type ProtoMessage interface {
	proto.Message
	MessageID() int32
	MessageName() string
	NodeName() string
	ModeName() string
}

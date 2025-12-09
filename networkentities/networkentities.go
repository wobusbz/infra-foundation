package networkentities

import (
	"infra-foundation/packet"
	"infra-foundation/protomessage"
)

type NetworkEntitieser interface {
	Send(pb protomessage.ProtoMessage) error
	SendPb(typ packet.Type, id int32, pb protomessage.ProtoMessage) error
	SendTypePb(typ packet.Type, pb protomessage.ProtoMessage) error
	SendPack(pack *packet.Packet) error
	Close() error
}

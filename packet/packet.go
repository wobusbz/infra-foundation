package packet

import (
	"fmt"
	"sync"
)

type Packeter interface {
	GetDataLen() uint32
	GetType() Type
	GetID() uint32
	GetData() []byte
	SetType(Type)
	SetID(uint32)
	SetData([]byte)
	SetDataLen(uint32)
}

type Type byte

const (
	Heartbeat Type = 0x01 + iota
	Data
	Connection
	DisConnection
	BindConnection
	InternalData
	ClientData
	NotifyData
	Invalid
)

var (
	packetPool = sync.Pool{New: func() any { return &Packet{} }}
)

type Packet struct {
	typ    Type
	id     int32
	sid    int64
	uid    int64
	length int32
	data   []byte
}

func New(typ Type, id int32, data []byte) *Packet {
	p := packetPool.Get().(*Packet)
	p.typ = typ
	p.length = int32(len(data))
	p.id = id
	p.data = data
	return p
}

func NewInternal(typ Type, id int32, sid int64, data []byte) *Packet {
	p := packetPool.Get().(*Packet)
	p.typ = typ
	p.length = int32(len(data))
	p.id = id
	p.sid = sid
	p.data = data
	return p
}

func (p *Packet) Free() {
	p.typ = 0
	p.length = 0
	p.id = 0
	p.sid = 0
	p.uid = 0
	p.data = nil
	packetPool.Put(p)
}

func (p *Packet) ID() int32 { return p.id }

func (p *Packet) SID() int64 { return p.sid }

func (p *Packet) UID() int64 { return p.uid }

func (p *Packet) Type() Type { return p.typ }

func (p *Packet) Data() []byte { return p.data }

func (p *Packet) String() string {
	return fmt.Sprintf("Type: %d, ID: %d, Length: %d, Sid: %d, Data: %s", p.typ, p.id, p.length, p.sid, string(p.data))
}

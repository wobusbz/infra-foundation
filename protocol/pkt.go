package protocol

import (
	"fmt"
	"sync"
)

type ClusterType byte

const (
	ClusterHeartbeat ClusterType = 0x01 + iota
	ClusterRequest
	ClusterHandshake   // Connection
	ClusterDisconnect  // DisConnection
	ClusterBindSession // BindConnection
	ClusterServiceCall // InternalData
	ClusterResponse    // ClientData
	ClusterPush        // NotifyData
	ClusterInvalid
)

var pktPool = sync.Pool{New: func() any { return &Pkt{} }}

type Pkt struct {
	typ  ClusterType
	id   int32
	sid  string
	uid  int64
	len  int32
	data []byte
}

func New(t ClusterType, id int32, data []byte) *Pkt {
	p := pktPool.Get().(*Pkt)
	p.typ = t
	p.id = id
	p.sid = ""
	p.len = int32(len(data))
	p.data = data
	return p
}

func NewWithSID(t ClusterType, id int32, sid string, data []byte) *Pkt {
	p := pktPool.Get().(*Pkt)
	p.typ = t
	p.id = id
	p.sid = sid
	p.len = int32(len(data))
	p.data = data
	return p
}

func (p *Pkt) Free() {
	p.typ = 0
	p.len = 0
	p.id = 0
	p.sid = ""
	p.uid = 0
	p.data = nil
	pktPool.Put(p)
}

func (p *Pkt) ID() int32 { return p.id }

func (p *Pkt) SID() string { return p.sid }

func (p *Pkt) UID() int64 { return p.uid }

func (p *Pkt) ClusterType() ClusterType { return p.typ }

func (p *Pkt) Data() []byte { return p.data }

func (p *Pkt) String() string {
	return fmt.Sprintf("type:%d id:%d len:%d sid:%s datalen:%d", p.typ, p.id, p.len, p.sid, len(p.data))
}

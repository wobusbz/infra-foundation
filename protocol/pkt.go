package protocol

import (
	"fmt"
	"sync"
)

type ClusterType byte

const (
	ClusterHeartbeat         ClusterType = 0x01 + iota
	ClusterHandshake                     // 0x02
	ClusterSessionDisconnect             // 0x03
	ClusterSessionBind                   // 0x04 (was ClusterSessionConnection)
	_                                    // 0x05 (was ClusterSessionInitialization, removed)
	ClusterRequest                       // 0x06
	ClusterResponse                      // 0x07
	ClusterPush                          // 0x08
	ClusterInvalid                       // 0x09
)

var pktPool = sync.Pool{New: func() any { return &Pkt{} }}

type Pkt struct {
	typ  ClusterType
	id   int32
	sid  string
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
	p.data = nil
	pktPool.Put(p)
}

func (p *Pkt) ID() int32 { return p.id }

func (p *Pkt) SID() string { return p.sid }

func (p *Pkt) ClusterType() ClusterType { return p.typ }

func (p *Pkt) Data() []byte { return p.data }

func (p *Pkt) String() string {
	return fmt.Sprintf("type:%d id:%d len:%d sid:%s datalen:%d", p.typ, p.id, p.len, p.sid, len(p.data))
}

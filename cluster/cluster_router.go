package cluster

import (
	"fmt"
	"infra-foundation/logx"
	"infra-foundation/protocol"
	"infra-foundation/queue"

	"github.com/cloudwego/netpoll"
)

type ClusterMsgHandler func(pk *protocol.Pkt, peer *PeerConn) error

type clusterMsgRouter struct {
	handlers map[protocol.ClusterType]ClusterMsgHandler
}

func newClusterMsgRouter() *clusterMsgRouter {
	return &clusterMsgRouter{handlers: make(map[protocol.ClusterType]ClusterMsgHandler)}
}

func (r *clusterMsgRouter) Register(typ protocol.ClusterType, h ClusterMsgHandler) {
	r.handlers[typ] = h
}

func (r *clusterMsgRouter) Dispatch(pk *protocol.Pkt, peer *PeerConn) error {
	if pk.ClusterType() == protocol.ClusterHeartbeat {
		peer.Conn.RefreshHeartbeat()
		return nil
	}
	h, ok := r.handlers[pk.ClusterType()]
	if !ok {
		return fmt.Errorf("unknown cluster type: %d", pk.ClusterType())
	}
	err := h(pk, peer)
	if err == nil {
		peer.Conn.RefreshHeartbeat()
	}
	return err
}

func processClusterMessage(peer *PeerConn, router *clusterMsgRouter, taskQueue *queue.TaskQueue, connection netpoll.Connection, tag string) error {
	r2, err := peer.codec.NextPacket(adaptNetpollReader(connection.Reader()))
	if err != nil {
		logx.Err.Printf("[%s/OnRequest] NextPacket error %v", tag, err)
		return fmt.Errorf("next packet: %w", err)
	}
	if r2 == nil {
		return nil
	}
	if err = taskQueue.Put(peer.queueKey, func() {
		pk, err := peer.codec.Unpack(r2)
		if err != nil {
			logx.Err.Printf("[%s/OnRequest] Unpack error %v", tag, err)
			return
		}
		if err := router.Dispatch(pk, peer); err != nil {
			logx.Err.Println(err)
		}
		pk.Free()
	}); err != nil {
		logx.War.Printf("[%s/OnRequest] overload, closing conn %s: %v", tag, peer.ID(), err)
		peer.Close()
		return err
	}
	return nil
}

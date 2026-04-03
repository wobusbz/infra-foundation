package cluster

import (
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/connmanager"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/session"

	"google.golang.org/protobuf/proto"
)

type ClusterHandler struct {
	connManager  *connmanager.SessionManager
	modelManager *model.ModelManager
	node         *Node
}

func (h *ClusterHandler) handleSessionClose(data []byte, connID int64) error {
	var pb clusterpb.N2MOnSessionClose
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("[ClusterHandler/handleSessionClose] ConnID[%d] Unmarshal %w", connID, err)
	}
	conn, ok := h.connManager.GetByID(pb.SessionID)
	if !ok {
		return fmt.Errorf("[ClusterHandler/handleSessionClose] ConnID[%d] SessionID: %d not found", connID, pb.SessionID)
	}
	return conn.Close()
}

func (h *ClusterHandler) handleBindConnection(data []byte, connID int64) error {
	var pb clusterpb.N2MOnSessionBindServer
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("[ClusterHandler/handleBindConnection] ConnID[%d] Unmarshal %w", connID, err)
	}
	conn, ok := h.connManager.GetByID(pb.SessionID)
	if !ok {
		conn = NewProxySession(session.NewSessionEntity(pb.SessionID, pb.UID), h.node)
		h.connManager.StoreSession(conn)
	}
	for name, id := range pb.GetServers() {
		conn.BindServers(name, id)
	}
	logx.Dbg.Printf("[ClusterHandler/handleBindConnection] ConnID[%d] SessionID: %d %v", connID, pb.SessionID, conn.Servers())
	return nil
}

func (h *ClusterHandler) handleInternalData(id int32, sid, connID int64, data []byte) error {
	if !model.IsLocalHandler(id) {
		return fmt.Errorf("[ClusterHandler/handleInternalData] ConnID[%d] MessageID: %d not found", connID, id)
	}
	conn, ok := h.connManager.GetByID(sid)
	if !ok {
		return fmt.Errorf("[ClusterHandler/handleInternalData] ConnID[%d] SessionID: %d not found", connID, sid)
	}
	return h.modelManager.Dispatch(conn, id, data)
}

func (h *ClusterHandler) handleClientData(sid, connID int64, data []byte) error {
	conn, ok := h.connManager.GetByID(sid)
	if !ok {
		return fmt.Errorf("[ClusterHandler/handleClientData] ConnID[%d] SessionID: %d not found", connID, sid)
	}
	sender, ok := conn.(interface{ SendData([]byte) error })
	if !ok {
		return fmt.Errorf("[ClusterHandler/handleClientData] ConnID[%d] 反射 SendData", connID)
	}
	return sender.SendData(data)
}

func (h *ClusterHandler) handleNotifyData(data []byte, connID int64) error {
	var pb clusterpb.N2MNotify
	if err := proto.Unmarshal(data, &pb); err != nil {
		return fmt.Errorf("[ClusterHandler/handleNotifyData] ConnID[%d] Unmarshal %w", connID, err)
	}

	if len(pb.SessionID) == 0 {
		return h.connManager.Range(func(s session.Session) error {
			sender, ok := s.(interface{ SendData([]byte) error })
			if !ok {
				return fmt.Errorf("[ClusterHandler/handleNotifyData] Range 反射 SendData")
			}
			return sender.SendData(pb.Plyload)
		})
	}

	var errs []error
	for _, sid := range pb.SessionID {
		conn, ok := h.connManager.GetByID(sid)
		if !ok {
			errs = append(errs, fmt.Errorf("[ClusterHandler/handleNotifyData] ConnID[%d] SessionID: %d not found", connID, sid))
			continue
		}
		sender, ok := conn.(interface{ SendData([]byte) error })
		if !ok {
			errs = append(errs, fmt.Errorf("[ClusterHandler/handleNotifyData] 反射 SendData"))
			continue
		}
		errs = append(errs, sender.SendData(pb.Plyload))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("[ClusterHandler/handleNotifyData] Notify error: %w", err)
	}
	return nil
}



package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ClusterFixedHeaderLen = 17 // PktLen(4) + Type(1) + RouteMsgID(4) + RequestID(8)
	ClusterMinFrameLen    = ClusterFixedHeaderLen + 2
	MaxPktLen             = 10 << 20
)

var ErrClusterWrongType = errors.New("cluster: wrong type")

type ClusterCodec struct {
	frame FrameCodec
}

func NewClusterCodec() *ClusterCodec {
	return &ClusterCodec{
		frame: NewFrameCodec(ClusterMinFrameLen, MaxPktLen),
	}
}

func (c *ClusterCodec) Pack(t ClusterType, routeMsgID int32, requestID uint64, sid string, payload []byte) ([]byte, error) {
	if t < ClusterHeartbeat || t >= ClusterInvalid {
		return nil, ErrClusterWrongType
	}

	sidBytes := []byte(sid)
	sidLen := len(sidBytes)

	payloadLen := uint32(len(payload))
	total := ClusterFixedHeaderLen + 2 + sidLen
	total += int(payloadLen)

	buf := GetBuf(total)

	off := 0
	binary.BigEndian.PutUint32(buf[off:off+4], uint32(total))
	off += 4
	buf[off] = byte(t)
	off += 1
	binary.BigEndian.PutUint32(buf[off:off+4], uint32(routeMsgID))
	off += 4
	binary.BigEndian.PutUint64(buf[off:off+8], requestID)
	off += 8

	binary.BigEndian.PutUint16(buf[off:off+2], uint16(sidLen))
	off += 2
	copy(buf[off:], sidBytes)
	off += sidLen

	copy(buf[off:], payload)
	return buf, nil
}

func (c *ClusterCodec) NextPacket(r Reader) (Reader, error) {
	frame, err := c.frame.Next(r)
	if err != nil {
		return nil, err
	}
	if frame == nil {
		return nil, nil
	}

	btyp, err := frame.Peek(5)
	if err != nil {
		_ = frame.Release()
		return nil, err
	}
	t := ClusterType(btyp[4])
	if t < ClusterHeartbeat || t >= ClusterInvalid {
		_ = frame.Release()
		return nil, ErrClusterWrongType
	}
	return frame, nil
}

func (c *ClusterCodec) Unpack(r Reader) (*Pkt, error) {
	defer r.Release()

	pktLen := r.Len()

	off := 0
	// PktLen (4 bytes) — already known from the sliced reader, skip
	_, err := r.Next(4)
	if err != nil {
		return nil, err
	}
	off += 4

	// Type (1 byte)
	btyp, err := r.Next(1)
	if err != nil {
		return nil, err
	}
	t := ClusterType(btyp[0])
	off += 1

	// RouteMsgID (4 bytes)
	bId, err := r.Next(4)
	if err != nil {
		return nil, err
	}
	id := int32(binary.BigEndian.Uint32(bId))
	off += 4

	// RequestID (8 bytes)
	bReqID, err := r.Next(8)
	if err != nil {
		return nil, err
	}
	requestID := binary.BigEndian.Uint64(bReqID)
	off += 8

	// sidLen + sid
	bSidLen, err := r.Next(2)
	if err != nil {
		return nil, err
	}
	sidLen := int(binary.BigEndian.Uint16(bSidLen))
	off += 2

	if sidLen > pktLen-off {
		return nil, fmt.Errorf("invalid sidLen %d > remaining %d", sidLen, pktLen-off)
	}
	if sidLen > 1024 {
		return nil, fmt.Errorf("sidLen %d too large (max 1024)", sidLen)
	}

	var sid string
	if sidLen > 0 {
		bSid, err := r.Next(sidLen)
		if err != nil {
			return nil, err
		}
		sid = string(bSid)
		off += sidLen
	}

	payload, err := r.ReadBinary(pktLen - off)
	if err != nil {
		return nil, err
	}

	return NewWithSID(t, id, requestID, sid, payload), nil
}

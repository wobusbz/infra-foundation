package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cloudwego/netpoll"
)

var (
	ErrClientPktTooShort = errors.New("client: packet too short")
	ErrClientPktInvalid  = errors.New("client: invalid packet length")
)

type ClientProtocol interface {
	NextPacket(reader netpoll.Reader) (msgID int32, payload []byte, err error)
	Pack(msgID int32, payload []byte) []byte
	UnpackAll(data []byte) (msgIDs []int32, payloads [][]byte, err error)
}

type ClientCodec struct{}

func NewClientCodec() *ClientCodec {
	return &ClientCodec{}
}

func (c *ClientCodec) NextPacket(reader netpoll.Reader) (msgID int32, payload []byte, err error) {
	if reader.Len() < 4 {
		return 0, nil, nil
	}
	bLen, err := reader.Peek(4)
	if err != nil {
		return 0, nil, fmt.Errorf("peek length: %w", err)
	}
	length := int(binary.BigEndian.Uint32(bLen))
	if length < 8 {
		return 0, nil, fmt.Errorf("invalid packet length %d", length)
	}
	if reader.Len() < length {
		return 0, nil, nil
	}
	buf, err := reader.Next(length)
	if err != nil {
		return 0, nil, fmt.Errorf("next packet: %w", err)
	}
	msgID = int32(binary.BigEndian.Uint32(buf[4:8]))
	payload = buf[8:]
	return msgID, payload, nil
}

func (c *ClientCodec) Pack(msgID int32, payload []byte) []byte {
	total := 8 + len(payload)
	buf := make([]byte, total)
	binary.BigEndian.PutUint32(buf[0:4], uint32(total))
	binary.BigEndian.PutUint32(buf[4:8], uint32(msgID))
	copy(buf[8:], payload)
	return buf
}

func (c *ClientCodec) Unpack(data []byte) (msgID int32, payload []byte, err error) {
	if len(data) < 8 {
		return 0, nil, ErrClientPktTooShort
	}
	total := int(binary.BigEndian.Uint32(data[0:4]))
	if total < 8 || total > len(data) {
		return 0, nil, ErrClientPktInvalid
	}
	msgID = int32(binary.BigEndian.Uint32(data[4:8]))
	payload = data[8:total]
	return msgID, payload, nil
}

func (c *ClientCodec) UnpackAll(data []byte) (msgIDs []int32, payloads [][]byte, err error) {
	off := 0
	for off < len(data) {
		if len(data)-off < 8 {
			break
		}
		total := int(binary.BigEndian.Uint32(data[off : off+4]))
		if total < 8 {
			return nil, nil, ErrClientPktInvalid
		}
		if off+total > len(data) {
			break
		}
		msgID := int32(binary.BigEndian.Uint32(data[off+4 : off+8]))
		payload := data[off+8 : off+total]
		msgIDs = append(msgIDs, msgID)
		payloads = append(payloads, payload)
		off += total
	}
	return msgIDs, payloads, nil
}

func (c *ClientCodec) PeekLength(data []byte) (int, error) {
	if len(data) < 4 {
		return 0, ErrClientPktTooShort
	}
	total := int(binary.BigEndian.Uint32(data[0:4]))
	if total < 8 {
		return 0, ErrClientPktInvalid
	}
	return total, nil
}

func (c *ClientCodec) PackTo(msgID int32, payload []byte, buf []byte) ([]byte, error) {
	total := 8 + len(payload)
	if len(buf) < total {
		return nil, fmt.Errorf("client: buffer too small %d < %d", len(buf), total)
	}
	binary.BigEndian.PutUint32(buf[0:4], uint32(total))
	binary.BigEndian.PutUint32(buf[4:8], uint32(msgID))
	copy(buf[8:], payload)
	return buf[:total], nil
}

// PackPooled packs using a pooled buffer to avoid heap allocation.
// The caller must ensure the returned slice is passed to a path that
// eventually calls protocol.PutBuf so the buffer can be recycled.
func (c *ClientCodec) PackPooled(msgID int32, payload []byte) []byte {
	total := 8 + len(payload)
	buf := GetBuf(total)
	binary.BigEndian.PutUint32(buf[0:4], uint32(total))
	binary.BigEndian.PutUint32(buf[4:8], uint32(msgID))
	copy(buf[8:], payload)
	return buf[:total]
}

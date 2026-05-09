package protocol

import (
	"encoding/binary"
	"fmt"
)

type ClientProtocol interface {
	NextPacket(reader Reader) (msgID int32, payload []byte, err error)
	Pack(msgID int32, payload []byte) []byte
	PackPooled(msgID int32, payload []byte) []byte
	UnpackAll(data []byte) (msgIDs []int32, payloads [][]byte, err error)
}

type ClientCodec struct {
	frame FrameCodec
}

func NewClientCodec() *ClientCodec {
	return &ClientCodec{
		frame: NewFrameCodec(8, MaxPktLen),
	}
}

func (c *ClientCodec) NextPacket(reader Reader) (msgID int32, payload []byte, err error) {
	frame, err := c.frame.Next(reader)
	if err != nil {
		return 0, nil, err
	}
	if frame == nil {
		return 0, nil, nil
	}
	defer frame.Release()

	if frame.Len() < 8 {
		return 0, nil, ErrFrameInvalidLength
	}

	head, err := frame.Next(8)
	if err != nil {
		return 0, nil, err
	}

	msgID = int32(binary.BigEndian.Uint32(head[4:8]))

	raw, err := frame.ReadBinary(frame.Len())
	if err != nil {
		return 0, nil, err
	}

	payload = make([]byte, len(raw))
	copy(payload, raw)
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
		return 0, nil, ErrFrameInvalidLength
	}
	total := int(binary.BigEndian.Uint32(data[0:4]))
	if total < 8 || total > len(data) {
		return 0, nil, ErrFrameInvalidLength
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
			return nil, nil, ErrFrameInvalidLength
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
		return 0, ErrFrameInvalidLength
	}
	total := int(binary.BigEndian.Uint32(data[0:4]))
	if total < 8 {
		return 0, ErrFrameInvalidLength
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

func (c *ClientCodec) PackPooled(msgID int32, payload []byte) []byte {
	total := 8 + len(payload)
	buf := GetBuf(total)
	binary.BigEndian.PutUint32(buf[0:4], uint32(total))
	binary.BigEndian.PutUint32(buf[4:8], uint32(msgID))
	copy(buf[8:], payload)
	return buf[:total]
}

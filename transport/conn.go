package transport

import (
	"errors"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/metric"
	"infra-foundation/protocol"
	"infra-foundation/session"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type Conn struct {
	wc io.WriteCloser
	*session.SessionEntity
	writeQ            chan []byte
	die               chan struct{}
	writeQueueMode    config.WriteQueueMode
	writeQueueTimeout time.Duration
	closed            atomic.Bool
	lastHeartBeatTime atomic.Int64
	wg                sync.WaitGroup
}

func NewConn(wc io.WriteCloser, id session.SessionID) *Conn {
	c := &Conn{
		wc:                wc,
		writeQ:            make(chan []byte, config.Default.TransportWriteQueueSize),
		die:               make(chan struct{}),
		writeQueueMode:    config.Default.TransportWriteQueueMode,
		writeQueueTimeout: config.Default.TransportWriteQueueTimeout,
		SessionEntity:     session.NewSessionEntity(id),
	}
	c.wg.Go(c.writeLoop)
	return c
}

func (c *Conn) SetClosed() bool {
	return c.closed.CompareAndSwap(false, true)
}

func (c *Conn) IsClosed() bool {
	return c.closed.Load()
}

func (c *Conn) HeartbeatAt() int64 {
	return c.lastHeartBeatTime.Load()
}

func (c *Conn) SetHeartbeatAt(now int64) {
	c.lastHeartBeatTime.Store(now)
}

func (c *Conn) RefreshHeartbeat() {
	c.SetHeartbeatAt(time.Now().Unix())
}

func (c *Conn) SendData(data []byte) error {
	if c.IsClosed() {
		return errors.New("send data: connection closed")
	}
	switch c.writeQueueMode {
	case config.WriteQueueModeBlock:
		select {
		case c.writeQ <- data:
			return nil
		case <-time.After(c.writeQueueTimeout):
			metric.CounterOf("transport_write_queue_timeout").Inc()
			return errors.New("send data: write queue timeout")
		}
	case config.WriteQueueModeBlockWithTimeout:
		select {
		case c.writeQ <- data:
			return nil
		case <-time.After(c.writeQueueTimeout):
			metric.CounterOf("transport_write_queue_timeout").Inc()
			return errors.New("send data: write queue timeout")
		}
	default:
		select {
		case c.writeQ <- data:
			return nil
		default:
			metric.CounterOf("transport_write_queue_dropped").Inc()
			return errors.New("send data: write queue full")
		}
	}
}

func (c *Conn) Close() error {
	if !c.SetClosed() {
		return nil
	}
	close(c.die)
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(c.writeQueueTimeout):
		logx.War.Printf("[transport.Conn/Close] writeLoop wait timeout, forcing close")
	}
	return nil
}

func (c *Conn) writeLoop() {
	const maxBatch = 16
	const maxBatchBytes = 64 * 1024
	defer func() {
		if c.wc == nil {
			return
		}
		if err := c.wc.Close(); err != nil {
			logx.Err.Println(err)
		}
	}()
	for {
		var buf []byte
		var ok bool
		select {
		case buf, ok = <-c.writeQ:
			if !ok {
				return
			}
		case <-c.die:
			for {
				select {
				case buf := <-c.writeQ:
					if len(buf) > 0 {
						protocol.PutBuf(buf)
					}
				default:
					return
				}
			}
		}

		if len(buf) == 0 {
			protocol.PutBuf(buf)
			continue
		}
		if c.IsClosed() {
			protocol.PutBuf(buf)
			continue
		}

		totalLen := len(buf)
		batch := make([][]byte, 1, maxBatch)
		batch[0] = buf

		for len(batch) < maxBatch && totalLen < maxBatchBytes {
			select {
			case next, ok := <-c.writeQ:
				if !ok {
					goto flush
				}
				if len(next) == 0 {
					protocol.PutBuf(next)
					continue
				}
				if c.IsClosed() {
					protocol.PutBuf(next)
					continue
				}
				batch = append(batch, next)
				totalLen += len(next)
			default:
				goto flush
			}
		}

	flush:
		metric.CounterOf("transport_write_batches").Inc()
		metric.HistogramOf("transport_write_batch_size").Observe(float64(len(batch)))

		if len(batch) == 1 {
			_, err := c.wc.Write(batch[0])
			protocol.PutBuf(batch[0])
			if err != nil {
				logx.Err.Println(err)
				return
			}
			metric.CounterOf("transport_packets_sent_total").Inc()
			metric.CounterOf("transport_bytes_sent_total").Add(uint64(len(batch[0])))
		} else {
			merged := protocol.GetBuf(totalLen)
			off := 0
			for _, b := range batch {
				copy(merged[off:], b)
				off += len(b)
				protocol.PutBuf(b)
			}
			_, err := c.wc.Write(merged[:off])
			if err != nil {
				protocol.PutBuf(merged)
				logx.Err.Println(err)
				return
			}
			metric.CounterOf("transport_packets_sent_total").Add(uint64(len(batch)))
			metric.CounterOf("transport_bytes_sent_total").Add(uint64(off))
			protocol.PutBuf(merged)
		}
		c.RefreshHeartbeat()
	}
}

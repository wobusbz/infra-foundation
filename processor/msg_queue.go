package processor

import (
	"context"
	"errors"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/metrics"
	"infra-foundation/pcall"
	"runtime"
	"sync"
	"sync/atomic"
)

func init() {
	if config.Default.ReaderQNum <= 0 {
		config.Default.ReaderQNum = int64(runtime.NumCPU())
	}
}

// workTask 用于消除 MsgQueue 中闭包分配的开销。
// 虽然 fn 本身仍可能逃逸到堆上，但 workTask 结构体可以通过 sync.Pool 复用。
type workTask struct {
	fn func()
}

var taskPool = sync.Pool{New: func() any { return &workTask{} }}

// MsgQueue 是多队列消息工作池，按 sessionID % ReaderQNum 哈希分发，
// 保证同一 Session 的消息顺序处理。
type MsgQueue struct {
	readerQ []chan *workTask
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closed  atomic.Bool
}

func NewMsgQueue() *MsgQueue {
	w := &MsgQueue{
		readerQ: make([]chan *workTask, config.Default.ReaderQNum),
	}
	for i := range w.readerQ {
		w.readerQ[i] = make(chan *workTask, config.Default.ReaderQLen)
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.wg.Go(w.runLoop)
	return w
}

func (w *MsgQueue) Put(id int64, cb func()) error {
	idx := id % (int64(config.Default.ReaderQNum))
	idxQ := w.readerQ[idx]

	if qLen := len(idxQ); qLen > config.Default.ReaderQWarnThreshold {
		logx.War.Printf("[MsgQueue] queue %d near full: %d", idx, qLen)
	}

	t := taskPool.Get().(*workTask)
	t.fn = cb

	ctx, cancel := context.WithTimeout(w.ctx, config.Default.ReaderQTimeout)
	defer cancel()
	select {
	case idxQ <- t:
		metrics.CounterOf("workmsg_task_queued_total").Inc()
	case <-ctx.Done():
		t.fn = nil
		taskPool.Put(t)
		metrics.CounterOf("workmsg_queue_full_total").Inc()
		return errors.New("[MsgQueue/Put] queue full, degraded")
	}
	return nil
}

func (w *MsgQueue) runLoop() {
	for i := range w.readerQ {
		q := w.readerQ[i]
		w.wg.Go(func() {
			for {
				select {
				case t := <-q:
					pcall.PcallF0(t.fn)
					t.fn = nil
					taskPool.Put(t)
				case <-w.ctx.Done():
					return
				}
			}
		})
	}
}

func (w *MsgQueue) Close() {
	if !w.closed.CompareAndSwap(false, true) {
		return
	}
	w.cancel()
	w.wg.Wait()
}

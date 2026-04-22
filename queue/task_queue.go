package queue

import (
	"context"
	"errors"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/metric"
	"infra-foundation/pcall"
	"sync"
	"sync/atomic"
)

var (
	counterQueued = metric.CounterOf("queue_task_queued")
	counterWaited = metric.CounterOf("queue_task_waited")
	counterFull   = metric.CounterOf("queue_full")
)

type task struct {
	fn func()
}

var taskPool = sync.Pool{New: func() any { return &task{} }}

type TaskQueue struct {
	queues        []chan *task
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	closed        atomic.Bool
	qnumMask      int64
	warnThreshold int
}

func NewTaskQueue() *TaskQueue {
	qnum := int64(1)
	for qnum < config.Default.ReaderQNum {
		qnum <<= 1
	}

	wt := config.Default.ReaderQWarnThreshold
	if wt <= 0 {
		wt = int(float64(config.Default.ReaderQLen) * 0.9)
	}
	mq := &TaskQueue{
		queues:        make([]chan *task, qnum),
		qnumMask:      qnum - 1,
		warnThreshold: wt,
	}
	for i := range mq.queues {
		mq.queues[i] = make(chan *task, config.Default.ReaderQLen)
	}
	mq.ctx, mq.cancel = context.WithCancel(context.Background())
	mq.wg.Go(mq.run)
	return mq
}

func hashID(id string) int64 {
	if len(id) == 0 {
		return 0
	}
	start := max(len(id)-8, 0)
	var h uint64 = 14695981039346656037
	for i := start; i < len(id); i++ {
		h ^= uint64(id[i])
		h *= 1099511628211
	}
	return int64(h & 0x7FFFFFFFFFFFFFFF)
}

func (mq *TaskQueue) Put(id string, fn func()) error {
	if mq.closed.Load() {
		return errors.New("queue closed")
	}

	idx := hashID(id) & mq.qnumMask
	ch := mq.queues[idx]
	qlen := len(ch)
	if qlen >= mq.warnThreshold {
		logx.War.Printf("queue %d full: %d/%d (%.1f%%)", idx, qlen, cap(ch), float64(qlen)*100/float64(cap(ch)))
	}

	t := taskPool.Get().(*task)
	t.fn = fn

	select {
	case ch <- t:
		counterQueued.Inc()
		return nil
	default:
	}

	ctx, cancel := context.WithTimeout(mq.ctx, config.Default.ReaderQTimeout)
	defer cancel()

	select {
	case ch <- t:
		counterQueued.Inc()
		counterWaited.Inc()
		return nil
	case <-ctx.Done():
		t.fn = nil
		taskPool.Put(t)
		counterFull.Inc()
		logx.Err.Printf("queue %d full, task dropped", idx)
		return errors.New("queue full")
	}
}

const batchSize = 64

func (mq *TaskQueue) run() {
	for i := range mq.queues {
		ch := mq.queues[i]
		mq.wg.Go(func() {
			for {
				select {
				case t := <-ch:
					pcall.PcallF0(t.fn)
					t.fn = nil
					taskPool.Put(t)

					for n := 1; n < batchSize; n++ {
						select {
						case t2 := <-ch:
							pcall.PcallF0(t2.fn)
							t2.fn = nil
							taskPool.Put(t2)
						default:
							n = batchSize
						}
					}

				case <-mq.ctx.Done():
					return
				}
			}
		})
	}
}

func (mq *TaskQueue) Close() {
	if !mq.closed.CompareAndSwap(false, true) {
		return
	}
	mq.cancel()
	mq.wg.Wait()
}

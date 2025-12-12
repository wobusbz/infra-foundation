package cluster

import (
	"context"
	"errors"
	"infra-foundation/pcall"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type WorkMessage struct {
	readerQ []chan func()
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closed  atomic.Bool
}

func newWorkMessage() *WorkMessage {
	w := &WorkMessage{
		readerQ: make([]chan func(), runtime.NumCPU()),
	}
	for i := range w.readerQ {
		w.readerQ[i] = make(chan func(), 1<<8)
	}
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.wg.Go(w.runLoop)
	return w
}

func (w *WorkMessage) Put(id int64, cb func()) error {
	ctx, cancel := context.WithTimeout(w.ctx, time.Second*3)
	defer cancel()
	select {
	case w.readerQ[id%int64(len(w.readerQ))] <- cb:
	case <-ctx.Done():
		// TODO FATA  非常严重了
		return errors.New("[WorkMessage/Put] queue full")
	}
	return nil
}

func (w *WorkMessage) runLoop() {
	for i := range w.readerQ {
		w.wg.Go(func() {
			for {
				select {
				case cb := <-w.readerQ[i]:
					pcall.PcallF0(cb)
				case <-w.ctx.Done():
					return
				}
			}
		})
	}
}

func (w *WorkMessage) Close() {
	if !w.closed.CompareAndSwap(false, true) {
		return
	}
	w.cancel()
	w.wg.Wait()
}

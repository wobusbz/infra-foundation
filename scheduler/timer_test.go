package scheduler

import (
	"infra-foundation/logx"
	"testing"
	"time"
)

func TestPushTimer(t *testing.T) {
	s := NewScheduler()
	s.ScheduleTimer(time.Second, false, func() {
		logx.Dbg.Println("hello world 1111111111111")
	})
	s.ScheduleTimer(time.Second*5, true, func() {
		logx.Dbg.Println("hello world 2222222222222")
	})
	time.Sleep(time.Second * 21)
}

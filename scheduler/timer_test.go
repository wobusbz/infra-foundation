package scheduler

import (
	"log"
	"testing"
	"time"
)

func TestPushTimer(t *testing.T) {
	s := NewScheduler()
	s.PushInfiniteTimer(time.Second, false, func() {
		log.Println("hello world 1111111111111")
	})
	s.PushInfiniteTimer(time.Second*5, true, func() {
		log.Println("hello world 2222222222222")
	})
	time.Sleep(time.Second * 20)
}

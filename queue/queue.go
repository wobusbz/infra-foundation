package queue

import (
	"fmt"
	"runtime"
	"sync/atomic"
)

type Queue[T any] struct {
	head atomic.Pointer[node[T]]
	tail atomic.Pointer[node[T]]
}

type node[T any] struct {
	next    atomic.Pointer[node[T]]
	element T
}

func New[T any]() *Queue[T] {
	n := &node[T]{}
	q := &Queue[T]{}
	q.head.Store(n)
	q.tail.Store(n)
	return q
}

func (q *Queue[T]) Front() (T, bool) {
	var zero T
	head := q.head.Load()
	if head == nil {
		return zero, false
	}
	return head.element, true
}

func (q *Queue[T]) Back() (T, bool) {
	var zero T
	tail := q.tail.Load()
	if tail == nil {
		return zero, false
	}
	return tail.element, true
}

func (q *Queue[T]) Push(v T) {
	n := &node[T]{element: v}
	prev := q.head.Swap(n)
	prev.next.Store(n)
}

func (q *Queue[T]) Pop() (T, bool) {
	var zero T
	for {
		tail := q.tail.Load()
		next := tail.next.Load()
		if tail != q.tail.Load() {
			runtime.Gosched()
			continue
		}

		if next == nil {
			return zero, false
		}

		if q.tail.CompareAndSwap(tail, next) {
			el := next.element
			next.element = zero
			return el, true
		}
		runtime.Gosched()
	}
}

func (q *Queue[T]) PopSingleThread() (T, bool) {
	var zero T
	tail := q.tail.Load()
	next := tail.next.Load()
	if next == nil {
		return zero, false
	}
	q.tail.Store(next)
	el := next.element
	next.element = zero
	return el, true
}

func (q *Queue[T]) Poll() (T, bool) {
	var zero T
	for {
		tail := q.tail.Load()
		next := tail.next.Load()
		if tail != q.tail.Load() {
			runtime.Gosched()
			continue
		}

		if next == nil {
			return zero, false
		}
		return next.element, true
	}
}

func (q *Queue[T]) Empty() bool {
	tail := q.tail.Load()
	return tail.next.Load() == nil
}

func (q *Queue[T]) Print() {
	current := q.tail.Load()
	if current == nil {
		return
	}
	for current != nil {
		next := current.next.Load()
		if next != nil {
			fmt.Printf("%v\t", next.element)
		}
		current = next
	}
	fmt.Println()
}

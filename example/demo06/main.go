package main

import (
	"fmt"
	"iter"
	"slices"
)

func main() {
	seq := slices.Values([]int32{1, 2, 3, 4}) // seq 是 iter.Seq[int32]

	next, stop := iter.Pull(seq)
	defer stop()
	for {
		v, ok := next()
		if !ok {
			break
		}
		fmt.Println(v)
	}
}

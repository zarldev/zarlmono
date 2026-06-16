package zsync_test

import (
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/zsync"
)

func ExampleQueue() {
	q := zsync.NewQueue[string]()
	defer q.Close()

	_ = q.Push("first")
	_ = q.Push("second")

	one, _ := q.PopContext(context.Background())
	two, _ := q.Pop()

	fmt.Println(one)
	fmt.Println(two)
	// Output:
	// first
	// second
}

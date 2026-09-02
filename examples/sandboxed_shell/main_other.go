//go:build !linux

package main

import "fmt"

func main() {
	fmt.Println("sandboxed_shell requires Linux Landlock; example skipped on this platform.")
}

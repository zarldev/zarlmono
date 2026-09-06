// Command docmedia checks or synchronizes rendered documentation GIFs.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/zarldev/zarlmono/tools/docmedia"
)

func main() {
	root := flag.String("root", ".", "repository root")
	sync := flag.Bool("sync", false, "copy canonical GIFs to site/public")
	flag.Parse()

	if *sync {
		if err := docmedia.Sync(*root); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, "documentation GIFs synchronized")
		return
	}
	if err := docmedia.Check(*root); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, "documentation GIFs match canonical renders")
}

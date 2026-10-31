// Command zarlos is the zarlOS system helper.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "version":
		cmdVersion()
	case "fetch":
		cmdFetch(os.Args[2:])
	case "health":
		os.Exit(cmdHealth())
	case "theme":
		cmdTheme()
	case "palette":
		cmdPalette()
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "%s %s\n\n", danger.On("unknown command:"), text.On(os.Args[1]))
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Printf("%s %s\n\n", primary.On("zarlOS"), muted.On("v"+osVersion()))
	fmt.Println(text.On("Usage: zarlos <command>"))
	fmt.Println()
	fmt.Printf("  %-9s %s\n", primary.On("version"), text.On("print the zarlOS version"))
	fmt.Printf("  %-9s %s\n", primary.On("fetch"), text.On("show the centered system fetch (fastfetch)"))
	fmt.Printf("  %-9s %s\n", primary.On("health"), text.On("check the rice: tools, fonts, services, config"))
	fmt.Printf("  %-9s %s\n", primary.On("theme"), text.On("re-apply the Night Instrument CLI theme"))
	fmt.Printf("  %-9s %s\n", primary.On("palette"), text.On("print the 16-color palette"))
}

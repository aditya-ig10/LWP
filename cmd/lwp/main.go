package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aditya-ig10/LWP/internal/extract"
	"github.com/aditya-ig10/LWP/internal/repl"
)

func main() {
	if len(os.Args) < 2 {
		repl.Start()
		return
	}

	switch os.Args[1] {
	case "fetch":
		runFetch()
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: lwp [command]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  (no args)    interactive REPL")
	fmt.Fprintln(os.Stderr, "  fetch <url>  fetch page content as JSON")
	os.Exit(2)
}

func runFetch() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: lwp fetch <url>")
		os.Exit(2)
	}
	page, err := extract.Fetch(os.Args[2], 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(page, "", "  ")
	fmt.Println(string(out))
}

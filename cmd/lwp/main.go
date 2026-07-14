package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aditya-ig10/LWP/internal/extract"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "fetch" {
		fmt.Fprintln(os.Stderr, "usage: lwp fetch <url> [--pretty] [--timeout <sec>]")
		os.Exit(2)
	}

	url := os.Args[2]
	pretty := false
	timeout := 30 * time.Second

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--pretty":
			pretty = true
		case "--timeout":
			if i+1 < len(os.Args) {
				d, err := time.ParseDuration(os.Args[i+1] + "s")
				if err == nil {
					timeout = d
				}
				i++
			}
		}
	}

	page, err := extract.Tier1(url, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var out []byte
	if pretty {
		out, _ = json.MarshalIndent(page, "", "  ")
	} else {
		out, _ = json.Marshal(page)
	}

	fmt.Println(string(out))
}

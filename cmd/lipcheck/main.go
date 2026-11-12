package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/SimplyLiz/CodeMCP/internal/lip"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: lipcheck <symbol-uri>")
		os.Exit(2)
	}
	uri := os.Args[1]

	fmt.Fprintln(os.Stderr, "-- handshake --")
	info, err := lip.Handshake("lipcheck/0.1")
	if err != nil {
		fmt.Fprintln(os.Stderr, "handshake err:", err)
	}
	if info == nil {
		fmt.Fprintln(os.Stderr, "handshake returned nil")
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	fmt.Fprintln(os.Stderr, string(b))

	fmt.Fprintln(os.Stderr, "-- query_blast_radius_symbol --")
	fmt.Fprintln(os.Stderr, "URI:", uri)
	entry, qerr := lip.QueryBlastRadiusSymbol(uri, 0.6)
	if qerr != nil {
		fmt.Fprintln(os.Stderr, "err:", qerr)
	}
	if entry == nil {
		fmt.Fprintln(os.Stderr, "entry: nil")
		os.Exit(0)
	}
	b, _ = json.MarshalIndent(entry, "", "  ")
	fmt.Println(string(b))
}

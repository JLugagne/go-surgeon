package main

import (
	"context"
	"fmt"
	"os"

	"github.com/JLugagne/go-surgeon/internal/surgeon"
)

func main() {
	runner := surgeon.Setup()
	ctx := context.Background()

	if len(os.Args) < 2 {
		fmt.Println("usage: go-surgeon <command> [args]\nAvailable commands: execute, scaffold, list")
		os.Exit(1)
	}

	if err := runner(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

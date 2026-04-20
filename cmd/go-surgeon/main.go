package main

import (
	"context"
	"fmt"
	"os"

	"github.com/JLugagne/go-surgeon/internal/surgeon"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	runner := surgeon.Setup()
	ctx := context.Background()
	if err := runner(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

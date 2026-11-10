package main

import (
	"fmt"
	"os"

	"kubernova/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "kubernova:", err)
		os.Exit(1)
	}
}

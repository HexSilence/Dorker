package main

import (
	"github.com/HexSilence/dorker/internal/app"
	"os"
)

func main() { os.Exit(app.Run(os.Args[1:], app.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})) }

package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	idlPath := flag.String("idl", "", "path to the Anchor IDL")
	out := flag.String("out", "", "output package directory")
	pkg := flag.String("package", "", "package name, defaults to the IDL metadata name")
	programID := flag.String("program-id", "", "program id overriding the IDL address")
	flag.Parse()

	if *idlPath == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}

	err := run(options{
		idlPath:   *idlPath,
		out:       *out,
		pkg:       *pkg,
		programID: *programID,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "anchor-go:", err)
		os.Exit(1)
	}
}

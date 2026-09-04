package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/baron-shared-brain/baron/internal/managedruntime"
)

const catalogReadLimit int64 = 8 << 20

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: validate-managed-runtime-catalog <catalog.json>")
		os.Exit(2)
	}
	data, err := readCatalog(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "managed runtime catalog read failed: %v\n", err)
		os.Exit(1)
	}
	releases, err := managedruntime.ValidateBundleCatalog(data, catalogReadLimit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "managed runtime catalog validation failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Managed runtime catalog validated: %d releases, %d required components.\n", len(releases), len(managedruntime.RequiredBundleComponents()))
}

func readCatalog(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, catalogReadLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > catalogReadLimit {
		return nil, errors.New("catalog exceeds the 8 MiB safety limit")
	}
	return data, nil
}

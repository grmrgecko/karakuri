//go:build !unix

package main

import "os"

// preserveOwner is a no-op where files carry no Unix owner.
func preserveOwner(string, os.FileInfo) error { return nil }

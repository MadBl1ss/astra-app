//go:build !windows

package main

// Outside Windows the console is UTF-8, as strelkad prints.
func encodeOutput() (flush func()) { return func() {} }

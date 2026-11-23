//go:build !(gui && windows)

package main

import "errors"

// guiBuild is true only for the desktop build (-tags gui on Windows).
const guiBuild = false

func runWindow(string, func() string) error {
	return errors.New("this build has no window; rebuild with -tags gui on Windows")
}

func showError(string) {}

//go:build !darwin

package main

import "path/filepath"

func defaultChromium() string {
	return "chromium"
}

func defaultOutRoot() string {
	return filepath.Join(documentsDir(), "walk-and-talk")
}

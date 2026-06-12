//go:build unix && !darwin

package main

import "os/exec"

func openBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
}

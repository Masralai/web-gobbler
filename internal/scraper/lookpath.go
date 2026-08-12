package scraper

import "os/exec"

func lookPath(file string) (string, error) {
	return exec.LookPath(file)
}

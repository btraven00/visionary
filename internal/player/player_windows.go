//go:build windows

package player

import "os/exec"

// FindPlayer returns the best available audio player on Windows.
func FindPlayer() (string, []string) {
	players := []struct {
		name string
		args []string
	}{
		{"mpv", []string{"--no-video", "--really-quiet"}},
		{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}},
	}
	for _, p := range players {
		if path, err := exec.LookPath(p.name); err == nil {
			return path, p.args
		}
	}
	return "", nil
}

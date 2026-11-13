package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"wuzapi/internal/termuxbundle"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: termux_bundle_smoke BUNDLE.tar.zst")
		os.Exit(2)
	}
	archive, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	root, err := os.MkdirTemp("", "wuzapi-bundle-smoke-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(root)
	if err := termuxbundle.ExtractZstd(archive, root); err != nil {
		fail(err)
	}

	binDir := filepath.Join(root, "bin")
	libDir := filepath.Join(binDir, "lib")
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "LD_LIBRARY_PATH=") {
			env = append(env, entry)
		}
	}
	env = append(env, "LD_LIBRARY_PATH="+libDir+string(os.PathListSeparator)+os.Getenv("LD_LIBRARY_PATH"))
	linker := "/system/bin/linker64"
	if _, err := os.Stat(linker); err != nil {
		fail(fmt.Errorf("Android linker is required for linker-path smoke run: %w", err))
	}
	for _, command := range [][]string{{"ffmpeg", "-version"}} {
		binary := filepath.Join(binDir, command[0])
		for _, launch := range []struct {
			name string
			path string
			args []string
		}{
			{name: "direct", path: binary, args: command[1:]},
			{name: "linker64", path: linker, args: append([]string{binary}, command[1:]...)},
		} {
			cmd := exec.Command(launch.path, launch.args...)
			cmd.Env = env
			output, err := cmd.CombinedOutput()
			if err != nil {
				fail(fmt.Errorf("run extracted %s via %s: %w: %s", command[0], launch.name, err, output))
			}
			firstLine := string(output)
			if index := strings.IndexByte(firstLine, '\n'); index >= 0 {
				firstLine = firstLine[:index]
			}
			fmt.Printf("Extracted %s runs via %s: %s\n", command[0], launch.name, strings.TrimSpace(firstLine))
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

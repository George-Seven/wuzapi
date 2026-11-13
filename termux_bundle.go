package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"wuzapi/internal/termuxbundle"
)

// The Termux build script replaces this small, empty archive while building
// the executable, then restores it so generated dependency bundles stay out of
// the source tree.
//
//go:embed termux-resources.tar.zst
var termuxResourceArchive []byte

type termuxToolBundle struct {
	resourceDir string
	binDir      string
	libDir      string
	linker      string
}

var bundledTermuxTools *termuxToolBundle

func configureTermuxBinaries(executableDir string) error {
	prefix := os.Getenv("PREFIX")
	isTermux := os.Getenv("TERMUX_VERSION") != "" || strings.HasPrefix(prefix, "/data/data/com.termux/")
	if runtime.GOOS != "android" || !isTermux || len(termuxResourceArchive) == 0 {
		return nil
	}

	digest := sha256.Sum256(termuxResourceArchive)
	resourceRoot := filepath.Join(executableDir, ".termux-resources")
	resourceDir := filepath.Join(resourceRoot, hex.EncodeToString(digest[:]))
	if bundleHasAnyTool(resourceDir) {
		return activateTermuxBundle(resourceDir)
	}

	if err := os.MkdirAll(resourceRoot, 0700); err != nil {
		return fmt.Errorf("create Termux resource directory: %w", err)
	}
	stagingDir, err := os.MkdirTemp(resourceRoot, ".extract-")
	if err != nil {
		return fmt.Errorf("create Termux resource staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	if err := termuxbundle.ExtractZstd(termuxResourceArchive, stagingDir); err != nil {
		return err
	}
	if !bundleHasAnyTool(stagingDir) {
		// A normal non-Termux build embeds the empty placeholder archive. If it
		// is run on Termux, leave command lookup to the user's installed PATH.
		return nil
	}
	if err := validateTermuxBundle(stagingDir); err != nil {
		return err
	}

	if err := os.Rename(stagingDir, resourceDir); err != nil {
		// Another WuzAPI process may have extracted the same immutable bundle.
		if bundleHasAnyTool(resourceDir) {
			return activateTermuxBundle(resourceDir)
		}
		return fmt.Errorf("activate Termux resource bundle: %w", err)
	}
	return activateTermuxBundle(resourceDir)
}

func cleanupTermuxBinaries() error {
	bundle := bundledTermuxTools
	if bundle == nil {
		return nil
	}
	if err := os.RemoveAll(bundle.resourceDir); err != nil {
		return fmt.Errorf("remove Termux resource cache %s: %w", bundle.resourceDir, err)
	}
	bundledTermuxTools = nil
	return nil
}

func bundleToolExists(root, name string) bool {
	info, err := os.Stat(filepath.Join(root, "bin", name))
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func bundleHasAnyTool(root string) bool {
	return bundleToolExists(root, "ffmpeg")
}

func validateTermuxBundle(root string) error {
	if !bundleToolExists(root, "ffmpeg") {
		return fmt.Errorf("Termux resource bundle is missing executable bin/ffmpeg")
	}
	return nil
}

func activateTermuxBundle(resourceRoot string) error {
	if err := validateTermuxBundle(resourceRoot); err != nil {
		return err
	}
	binDir := filepath.Join(resourceRoot, "bin")
	bundledTermuxTools = &termuxToolBundle{
		resourceDir: resourceRoot,
		binDir:      binDir,
		libDir:      filepath.Join(binDir, "lib"),
		linker:      androidLinkerForBundledTools(),
	}
	return nil
}

// termuxCommand runs bundled tools by absolute path and exposes their bundled
// shared libraries only to that child process. Other commands use normal PATH
// lookup and inherit WuzAPI's unmodified environment.
func termuxCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if bundledTermuxTools != nil && filepath.Base(name) == name {
		path := filepath.Join(bundledTermuxTools.binDir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			env := os.Environ()
			command, commandArgs := path, args
			if bundledTermuxTools.linker != "" {
				command = bundledTermuxTools.linker
				commandArgs = append([]string{path}, args...)
			}
			cmd := exec.CommandContext(ctx, command, commandArgs...)
			cmd.Env = withEnvValue(env, "LD_LIBRARY_PATH", prependEnvPath(
				envValue(env, "LD_LIBRARY_PATH"), bundledTermuxTools.libDir))
			return cmd
		}
	}
	return exec.CommandContext(ctx, name, args...)
}

// Android API 29 and newer require the system linker to launch these bundled
// binaries. This follows the API-level check used by wuzapi-mod-newer.
func androidLinkerForBundledTools() string {
	if runtime.GOOS != "android" {
		return ""
	}
	output, err := exec.Command("getprop", "ro.build.version.sdk").Output()
	if err != nil {
		return ""
	}
	apiLevel, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || apiLevel <= 28 {
		return ""
	}
	switch runtime.GOARCH {
	case "arm64", "amd64":
		return "/system/bin/linker64"
	case "arm", "386":
		return "/system/bin/linker"
	default:
		return ""
	}
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok && name == key {
			return value
		}
	}
	return ""
}

func withEnvValue(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name != key {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

func prependEnvPath(current, prefix string) string {
	for _, entry := range filepath.SplitList(current) {
		if entry == prefix {
			return current
		}
	}
	if current == "" {
		return prefix
	}
	return prefix + string(os.PathListSeparator) + current
}

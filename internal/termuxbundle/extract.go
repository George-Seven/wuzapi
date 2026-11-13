package termuxbundle

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// ExtractZstd extracts the bundled bin tree. It accepts only regular files and
// directories beneath bin, preventing archive paths from escaping destination.
func ExtractZstd(data []byte, destination string) error {
	decoder, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderConcurrency(1))
	if err != nil {
		return fmt.Errorf("open embedded Zstandard archive: %w", err)
	}
	defer decoder.Close()

	if err := os.MkdirAll(destination, 0700); err != nil {
		return fmt.Errorf("create extraction directory: %w", err)
	}
	reader := tar.NewReader(decoder)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read embedded tar: %w", err)
		}

		name := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || (name != "bin" && !strings.HasPrefix(name, "bin"+string(filepath.Separator))) {
			return fmt.Errorf("unsafe path in embedded archive: %q", header.Name)
		}
		path := filepath.Join(destination, name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0755); err != nil {
				return fmt.Errorf("create bundled directory %q: %w", name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return fmt.Errorf("create directory for bundled file %q: %w", name, err)
			}
			mode := os.FileMode(header.Mode) & 0755
			file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return fmt.Errorf("create bundled file %q: %w", name, err)
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("extract bundled file %q: %w", name, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close bundled file %q: %w", name, closeErr)
			}
		default:
			return fmt.Errorf("unsupported file type in embedded archive: %q", header.Name)
		}
	}
}

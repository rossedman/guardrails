package tools

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/ulikunitz/xz"
)

// maxBinary bounds an extracted executable, guarding against archive bombs.
var maxBinary int64 = 512 << 20

// extract copies the executable at member inside the archive file src to
// dst. For raw artifacts src is the executable itself.
func extract(src, format, member, dst string) error {
	switch format {
	case FormatRaw:
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		return writeExecutable(dst, f)
	case FormatZip:
		return extractZip(src, member, dst)
	case FormatTarGz, FormatTarXz:
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		var r io.Reader
		if format == FormatTarGz {
			gz, err := gzip.NewReader(f)
			if err != nil {
				return fmt.Errorf("gzip: %w", err)
			}
			defer func() { _ = gz.Close() }()
			r = gz
		} else {
			xr, err := xz.NewReader(f)
			if err != nil {
				return fmt.Errorf("xz: %w", err)
			}
			r = xr
		}
		return extractTar(r, member, dst)
	default:
		return fmt.Errorf("unknown archive format %q", format)
	}
}

func extractTar(r io.Reader, member, dst string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s not found in archive", member)
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if h.Typeflag == tar.TypeReg && path.Clean(h.Name) == path.Clean(member) {
			return writeExecutable(dst, tr)
		}
	}
}

func extractZip(src, member, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if path.Clean(f.Name) != path.Clean(member) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		return writeExecutable(dst, rc)
	}
	return fmt.Errorf("%s not found in archive", member)
}

func writeExecutable(dst string, r io.Reader) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxBinary+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxBinary {
		return fmt.Errorf("executable exceeds %d bytes", maxBinary)
	}
	return nil
}

// Extract copies the executable at member inside the archive src to dst.
// It is exported for the lock generator, which verifies every artifact.
func Extract(src, format, member, dst string) error {
	return extract(src, format, member, dst)
}

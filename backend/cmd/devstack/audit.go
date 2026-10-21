package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func auditImage(path, directory string) error {
	secrets := [][]byte{}
	for _, name := range []string{"admin_password", "migrator_password", "runtime_password"} {
		value, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || len(value) != 64 {
			return errors.New("invalid credential scan input")
		}
		secrets = append(secrets, value)
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("image archive could not be read")
	}
	defer file.Close()
	return scanArchive(file, secrets, 0)
}

func scanArchive(reader io.Reader, secrets [][]byte, depth int) error {
	if depth > 5 {
		return errors.New("image archive nesting limit exceeded")
	}
	archive := tar.NewReader(reader)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("invalid image tar archive")
		}
		data, err := io.ReadAll(io.LimitReader(archive, (128<<20)+1))
		if err != nil || len(data) > 128<<20 {
			return errors.New("image layer scan size limit exceeded")
		}
		for _, value := range secrets {
			if bytes.Contains(data, value) {
				return errors.New("credential found in image layer")
			}
		}
		if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}) {
			return errors.New("zstd image layers require a supported decoder before scanning")
		}
		if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
			compressed, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return err
			}
			err = scanArchive(compressed, secrets, depth+1)
			_ = compressed.Close()
			if err != nil {
				return err
			}
		} else if strings.HasSuffix(header.Name, ".tar") || (len(data) > 262 && string(data[257:262]) == "ustar") {
			if err = scanArchive(bytes.NewReader(data), secrets, depth+1); err != nil {
				return err
			}
		}
	}
}

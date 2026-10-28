package main

import (
	"archive/tar"
	"bufio"
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
		// Bound individual entries while scanning in constant memory. Database
		// layers routinely exceed 128 MiB, so buffering whole layers is unsuitable.
		const maxEntrySize = 2 << 30
		if header.Size > maxEntrySize {
			return errors.New("image layer scan size limit exceeded")
		}
		dataReader := bufio.NewReader(io.TeeReader(archive, &credentialWriter{secrets: secrets}))
		data, peekErr := dataReader.Peek(512)
		if peekErr != nil && peekErr != io.EOF {
			return peekErr
		}
		if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x28, 0xb5, 0x2f, 0xfd}) {
			return errors.New("zstd image layers require a supported decoder before scanning")
		}
		if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
			compressed, err := gzip.NewReader(dataReader)
			if err != nil {
				return err
			}
			err = scanArchive(compressed, secrets, depth+1)
			_ = compressed.Close()
			if err != nil {
				return err
			}
		} else if strings.HasSuffix(header.Name, ".tar") || (len(data) > 262 && string(data[257:262]) == "ustar") {
			if err = scanArchive(dataReader, secrets, depth+1); err != nil {
				return err
			}
		}
		// Drain trailers and ordinary files through the credential detector too.
		if _, err := io.Copy(io.Discard, dataReader); err != nil {
			return err
		}
	}
}

type credentialWriter struct {
	secrets [][]byte
	tail    []byte
}

func (w *credentialWriter) Write(data []byte) (int, error) {
	window := append(w.tail, data...)
	keep := 0
	for _, secret := range w.secrets {
		if len(secret) == 0 {
			continue
		}
		if bytes.Contains(window, secret) {
			return 0, errors.New("credential found in image layer")
		}
		if len(secret)-1 > keep {
			keep = len(secret) - 1
		}
	}
	if keep > len(window) {
		keep = len(window)
	}
	w.tail = append(w.tail[:0], window[len(window)-keep:]...)
	return len(data), nil
}

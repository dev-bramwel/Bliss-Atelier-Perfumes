package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestCredentialScanIncludesCompressedLayers(t *testing.T) {
	pack := func(name string, body []byte) []byte {
		var data bytes.Buffer
		writer := tar.NewWriter(&data)
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return data.Bytes()
	}
	secret := []byte("synthetic-credential-detection-test")
	for _, compressed := range []bool{false, true} {
		for _, leak := range []bool{false, true} {
			body := []byte("clean layer")
			if leak {
				body = secret
			}
			layer := pack("app/config", body)
			name := "layer.tar"
			if compressed {
				var zipped bytes.Buffer
				writer := gzip.NewWriter(&zipped)
				if _, err := writer.Write(layer); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				layer = zipped.Bytes()
				name = "blobs/sha256/synthetic"
			}
			err := scanArchive(bytes.NewReader(pack(name, layer)), [][]byte{secret}, 0)
			if (err != nil) != leak {
				t.Fatalf("compressed=%v leak=%v error=%v", compressed, leak, err)
			}
		}
	}
}

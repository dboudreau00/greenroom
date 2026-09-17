package kube

import (
	"archive/tar"
	"io"
	"time"
)

type tarWriter struct {
	tw *tar.Writer
}

func newTarWriter(w io.Writer) *tarWriter { return &tarWriter{tw: tar.NewWriter(w)} }

func (t *tarWriter) WriteHeader(name string, mode, size int64) error {
	return t.tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: size, ModTime: time.Now(), Typeflag: tar.TypeReg})
}

func (t *tarWriter) Write(b []byte) (int, error) { return t.tw.Write(b) }

func (t *tarWriter) Close() error { return t.tw.Close() }

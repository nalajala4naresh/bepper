package disk

import (
	"compress/gzip"
	"os"
)

func newGzipWriter(f *os.File) *gzip.Writer {
	return gzip.NewWriter(f)
}

// compressFileWriter gzip-compresses writes into f. Close flushes the gzip
// stream before closing the underlying file, so the file is only ever a
// valid gzip stream once Close has succeeded.
type compressFileWriter struct {
	gz *gzip.Writer
	f  *os.File
}

func (w *compressFileWriter) Write(p []byte) (int, error) {
	return w.gz.Write(p)
}

func (w *compressFileWriter) Close() error {
	if err := w.gz.Close(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

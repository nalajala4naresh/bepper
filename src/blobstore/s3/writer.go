package s3

import (
	"compress/gzip"
	"io"
)

// streamWriter gzip-compresses writes into an io.Pipe whose reader end is
// being uploaded to S3 by a background goroutine. Close flushes the gzip
// stream, closes the pipe, and waits for the upload to finish so callers
// know whether the blob was actually committed.
type streamWriter struct {
	gz    *gzip.Writer
	pw    *io.PipeWriter
	errCh chan error
}

func newStreamWriter(pw *io.PipeWriter, errCh chan error) *streamWriter {
	return &streamWriter{gz: gzip.NewWriter(pw), pw: pw, errCh: errCh}
}

func (w *streamWriter) Write(p []byte) (int, error) {
	return w.gz.Write(p)
}

func (w *streamWriter) Close() error {
	if err := w.gz.Close(); err != nil {
		w.pw.CloseWithError(err)
		<-w.errCh
		return err
	}
	if err := w.pw.Close(); err != nil {
		return err
	}
	return <-w.errCh
}

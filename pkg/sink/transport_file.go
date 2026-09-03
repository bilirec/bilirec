package sink

import (
	"context"
	"io"
)

type FileTransport struct {
	writer io.WriteCloser
}

func NewFileTransport(writer io.WriteCloser) *FileTransport {
	return &FileTransport{writer: writer}
}

func (t *FileTransport) Consume(batch []byte) error {
	if len(batch) == 0 {
		return nil
	}
	_, err := t.writer.Write(batch)
	return err
}

func (t *FileTransport) Close(context.Context) error {
	return t.writer.Close()
}

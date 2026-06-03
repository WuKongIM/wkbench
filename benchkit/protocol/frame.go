package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

var ErrFrameTooLarge = errors.New("plugin frame too large")

type FrameWriter struct {
	w io.Writer
}

func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{w: w}
}

func (w *FrameWriter) WriteFrame(frame *Frame) error {
	payload, err := proto.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.w.Write(header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if _, err := w.w.Write(payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}
	return nil
}

type FrameReader struct {
	r        io.Reader
	maxBytes uint32
}

func NewFrameReader(r io.Reader, maxBytes int) *FrameReader {
	return &FrameReader{r: r, maxBytes: uint32(maxBytes)}
}

func (r *FrameReader) ReadFrame() (*Frame, error) {
	var header [4]byte
	if _, err := io.ReadFull(r.r, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > r.maxBytes {
		return nil, ErrFrameTooLarge
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r.r, payload); err != nil {
		return nil, err
	}
	var frame Frame
	if err := proto.Unmarshal(payload, &frame); err != nil {
		return nil, fmt.Errorf("unmarshal frame: %w", err)
	}
	return &frame, nil
}

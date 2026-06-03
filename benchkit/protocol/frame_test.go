package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameCodecRoundTripsMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)
	first := &Frame{RequestId: "one", Body: &Frame_HandshakeRequest{HandshakeRequest: &HandshakeRequest{HostProtocol: "wkbench.plugin/v1"}}}
	second := &Frame{RequestId: "two", Body: &Frame_ListUnitsRequest{ListUnitsRequest: &ListUnitsRequest{}}}
	if err := writer.WriteFrame(first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := writer.WriteFrame(second); err != nil {
		t.Fatalf("write second: %v", err)
	}

	reader := NewFrameReader(&buf, 1024)
	gotFirst, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	gotSecond, err := reader.ReadFrame()
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if gotFirst.RequestId != "one" || gotSecond.RequestId != "two" {
		t.Fatalf("unexpected ids: %q %q", gotFirst.RequestId, gotSecond.RequestId)
	}
}

func TestFrameReaderRejectsOversizedFrame(t *testing.T) {
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)
	if err := writer.WriteFrame(&Frame{RequestId: "too-big", Body: &Frame_Error{Error: &Error{Message: "payload"}}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	reader := NewFrameReader(&buf, 1)
	_, err := reader.ReadFrame()
	if err == nil {
		t.Fatal("expected oversized frame error")
	}
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("error = %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameRoundTripArtifactChunk(t *testing.T) {
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)
	want := &Frame{
		RequestId:      "run-1",
		RunId:          "scenario-1",
		UnitInstanceId: "collector",
		Body: &Frame_ArtifactChunk{ArtifactChunk: &ArtifactChunk{
			Handle:   "artifact-1",
			Sequence: 2,
			Data:     []byte("payload"),
		}},
	}

	if err := writer.WriteFrame(want); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	got, err := NewFrameReader(&buf, 1024).ReadFrame()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	chunk := got.GetArtifactChunk()
	if chunk == nil {
		t.Fatalf("artifact chunk frame missing: %#v", got)
	}
	if chunk.GetHandle() != "artifact-1" || chunk.GetSequence() != 2 || string(chunk.GetData()) != "payload" {
		t.Fatalf("unexpected chunk: %#v", chunk)
	}
}

func TestFrameReaderRejectsNegativeMaxBytes(t *testing.T) {
	var buf bytes.Buffer
	writer := NewFrameWriter(&buf)
	if err := writer.WriteFrame(&Frame{RequestId: "negative-max", Body: &Frame_Error{Error: &Error{Message: "payload"}}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	reader := NewFrameReader(&buf, -1)
	_, err := reader.ReadFrame()
	if err == nil {
		t.Fatal("expected oversized frame error")
	}
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("error = %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameReaderReturnsEOF(t *testing.T) {
	reader := NewFrameReader(bytes.NewReader(nil), 1024)
	_, err := reader.ReadFrame()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want EOF", err)
	}
}

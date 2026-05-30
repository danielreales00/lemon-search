package onnx

import (
	"context"
	"testing"
)

func TestNewRejectsEmptyModelPath(t *testing.T) {
	t.Parallel()
	if _, err := New(context.Background(), "", "", 1); err == nil {
		t.Fatal("New(\"\") = nil error, want a non-empty-path error")
	}
}

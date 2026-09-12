package pwlsp

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadMessageReadsOneFramedBody(t *testing.T) {
	stream := bufio.NewReader(strings.NewReader("Content-Length: 2\r\n\r\n{}Content-Length: 4\r\n\r\n[1,2]"))

	first, err := readMessage(stream)
	if err != nil {
		t.Fatalf("first message: %v", err)
	}
	if string(first) != "{}" {
		t.Fatalf("first = %q, want {}", first)
	}
	second, err := readMessage(stream)
	if err != nil {
		t.Fatalf("second message: %v", err)
	}
	if string(second) != "[1,2" {
		t.Fatalf("second = %q, want exactly the declared length", second)
	}
}

func TestReadMessageIgnoresOtherHeaders(t *testing.T) {
	// The base protocol also defines Content-Type, and a client may send
	// headers this server does not implement. Skipping them is required; only
	// a missing length is fatal.
	stream := bufio.NewReader(strings.NewReader("Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: 2\r\n\r\n{}"))

	body, err := readMessage(stream)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if string(body) != "{}" {
		t.Fatalf("body = %q", body)
	}
}

func TestReadMessageIsCaseInsensitiveAboutTheHeaderName(t *testing.T) {
	stream := bufio.NewReader(strings.NewReader("content-length: 2\r\n\r\n{}"))

	if _, err := readMessage(stream); err != nil {
		t.Fatalf("readMessage: %v", err)
	}
}

func TestReadMessageWithNoLengthIsFatal(t *testing.T) {
	// Without a length the reader cannot find where the next message starts,
	// so continuing would desynchronize the stream silently.
	stream := bufio.NewReader(strings.NewReader("X-Nothing: 1\r\n\r\n{}"))

	_, err := readMessage(stream)
	var header *errContentLength
	if !errors.As(err, &header) {
		t.Fatalf("err = %v, want a content-length error", err)
	}
}

func TestReadMessageReportsEOFAtTheEndOfTheStream(t *testing.T) {
	stream := bufio.NewReader(strings.NewReader(""))

	if _, err := readMessage(stream); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want EOF", err)
	}
}

func TestWriteMessageFramesTheEncodedBody(t *testing.T) {
	var out bytes.Buffer

	if err := writeMessage(&out, map[string]int{"a": 1}); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	if out.String() != "Content-Length: 7\r\n\r\n{\"a\":1}" {
		t.Fatalf("framed = %q", out.String())
	}
}

package termcap

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseDA1(t *testing.T) {
	tests := []struct {
		name string
		resp []byte
		want bool
	}{
		{
			name: "sixel supported - attribute 4 present",
			resp: []byte("\x1b[?1;2;4c"),
			want: true,
		},
		{
			name: "sixel supported - attribute 4 only",
			resp: []byte("\x1b[?4c"),
			want: true,
		},
		{
			name: "sixel supported - attribute 4 at end",
			resp: []byte("\x1b[?1;2;3;4c"),
			want: true,
		},
		{
			name: "sixel supported - attribute 4 at start",
			resp: []byte("\x1b[?4;6;7c"),
			want: true,
		},
		{
			name: "no sixel - attribute 4 absent",
			resp: []byte("\x1b[?1;2;6c"),
			want: false,
		},
		{
			name: "no sixel - empty params",
			resp: []byte("\x1b[?c"),
			want: false,
		},
		{
			name: "no sixel - too short",
			resp: []byte("abc"),
			want: false,
		},
		{
			name: "no sixel - no question mark",
			resp: []byte("\x1b[1;2;4c"),
			want: false,
		},
		{
			name: "no sixel - nil input",
			resp: nil,
			want: false,
		},
		{
			name: "no sixel - number 40 is not 4",
			resp: []byte("\x1b[?1;40;6c"),
			want: false,
		},
		{
			name: "no sixel - number 14 is not 4",
			resp: []byte("\x1b[?14;2;6c"),
			want: false,
		},
		{
			name: "sixel - typical xterm response",
			resp: []byte("\x1b[?62;4;6;9;22c"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDA1(tt.resp)
			if got != tt.want {
				t.Errorf("parseDA1(%q) = %v, want %v", tt.resp, got, tt.want)
			}
		})
	}
}

func TestReadDA1Response(t *testing.T) {
	t.Run("valid response", func(t *testing.T) {
		input := "\x1b[?1;2;4c"
		r := strings.NewReader(input)

		resp, err := readDA1Response(r, time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(resp, []byte(input)) {
			t.Errorf("got %q, want %q", resp, input)
		}
	})

	t.Run("response with leading garbage", func(t *testing.T) {
		// Some terminals echo characters before the response.
		input := "garbage\x1b[?4;6c"
		r := strings.NewReader(input)

		resp, err := readDA1Response(r, time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []byte("\x1b[?4;6c")
		if !bytes.Equal(resp, want) {
			t.Errorf("got %q, want %q", resp, want)
		}
	})

	t.Run("timeout on no response", func(t *testing.T) {
		// A reader that blocks forever.
		r, _ := io.Pipe()
		defer r.Close()

		_, err := readDA1Response(r, 50*time.Millisecond)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "timeout") {
			t.Errorf("expected timeout error, got: %v", err)
		}
	})

	t.Run("EOF before response complete", func(t *testing.T) {
		input := "\x1b[?1;2" // No terminating 'c'
		r := strings.NewReader(input)

		_, err := readDA1Response(r, 100*time.Millisecond)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestProbeNonTerminal(t *testing.T) {
	// Use a pipe fd which is not a terminal.
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()

	// Fd 9999 is almost certainly not a terminal.
	caps, err := Probe(9999, r, w)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if caps.SixelSupported {
		t.Error("expected SixelSupported=false for non-terminal fd")
	}
	if caps.WidthCells != 0 || caps.HeightCells != 0 {
		t.Errorf("expected zero dimensions, got %dx%d", caps.WidthCells, caps.HeightCells)
	}
}

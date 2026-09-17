package transcribe

import (
	"context"
	"encoding/binary"
	"os"
	"strings"
	"testing"
	"time"
)

// RECGO_REALTIME_E2E=<16 kHz mono s16le wav> RECGO_STT_ENDPOINT=https://host/v1
// go test ./internal/transcribe -run Realtime -v
func TestRealtimeEndToEnd(t *testing.T) {
	wav := os.Getenv("RECGO_REALTIME_E2E")
	endpoint := os.Getenv("RECGO_STT_ENDPOINT")
	if wav == "" || endpoint == "" {
		t.Skip("set RECGO_REALTIME_E2E and RECGO_STT_ENDPOINT")
	}
	data, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" || binary.LittleEndian.Uint32(data[24:]) != sampleRate {
		t.Fatalf("%s: need a 16 kHz RIFF wav", wav)
	}
	pcm := data[44:]
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s := NewFeed(ctx, Config{Endpoint: endpoint, APIKey: os.Getenv("LLM_API_KEY"), Realtime: true})
	if err := s.StartFeed(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	// Feed at 4x real time, then a tail of silence so server VAD closes the last utterance.
	go func() {
		step := sampleRate * bytesPerSample / 10
		for i := 0; i < len(pcm); i += step {
			end := i + step
			if end > len(pcm) {
				end = len(pcm)
			}
			s.Feed(pcm[i:end])
			time.Sleep(25 * time.Millisecond)
		}
		silence := make([]byte, sampleRate*bytesPerSample*2)
		for i := 0; i < 5; i++ {
			s.Feed(silence)
			time.Sleep(200 * time.Millisecond)
		}
	}()
	var partials, finals []string
	deadline := time.After(80 * time.Second)
	for {
		select {
		case st, ok := <-s.Updates():
			if !ok {
				t.Fatalf("feed closed; partials=%v finals=%v", partials, finals)
			}
			if st.Err != nil {
				t.Fatalf("realtime error: %v", st.Err)
			}
			if st.PassText != "" {
				finals = append(finals, st.PassText)
				t.Logf("final  %q [%d..%d]", st.PassText, st.PassStartSample, st.PassEndSample)
				if strings.Contains(strings.ToLower(strings.Join(finals, " ")), "working") {
					t.Logf("partials seen: %d", len(partials))
					return
				}
			} else if st.Fast != "" {
				partials = append(partials, st.Fast)
				t.Logf("partial %q", st.Fast)
			}
		case <-deadline:
			t.Fatalf("no final transcript with 'working'; partials=%v finals=%v", partials, finals)
		}
	}
}

func TestRealtimeURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://llm.decent.dev/v1":               "wss://llm.decent.dev/v1/realtime?intent=transcription",
		"http://127.0.0.1:8000/v1/":               "ws://127.0.0.1:8000/v1/realtime?intent=transcription",
		"wss://x/v1/realtime?intent=conversation": "wss://x/v1/realtime?intent=conversation",
	} {
		got, err := RealtimeURL(in)
		if err != nil || got != want {
			t.Errorf("RealtimeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestUpsample16to24(t *testing.T) {
	in := make([]byte, 0)
	for _, v := range []int16{0, 300, 600, 900} {
		in = binary.LittleEndian.AppendUint16(in, uint16(v))
	}
	var carry int16
	have := false
	out := upsample16to24(in, &carry, &have)
	if len(out) != 3*4/2*bytesPerSample-bytesPerSample*0 && len(out)/bytesPerSample != 4 {
		t.Fatalf("got %d samples", len(out)/bytesPerSample)
	}
	got := []int16{}
	for i := 0; i+1 < len(out); i += 2 {
		got = append(got, int16(binary.LittleEndian.Uint16(out[i:])))
	}
	if got[0] != 0 || got[1] != 200 || got[2] != 400 || got[3] != 600 {
		t.Fatalf("interpolation wrong: %v", got)
	}
	if !have || carry != 900 {
		t.Fatalf("carry = %d, have = %v", carry, have)
	}
}

package tab

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRate = 100

func pcmWav(seconds float64, quiet ...[2]float64) []byte {
	n := int(seconds * testRate)
	pcm := make([]byte, n*2)
	for i := 0; i < n; i++ {
		sec := float64(i) / testRate
		v := int16(8000)
		for _, q := range quiet {
			if sec >= q[0] && sec < q[1] {
				v = 0
			}
		}
		if i%2 == 1 {
			v = -v
		}
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	fmtChunk := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtChunk[0:], 1)
	binary.LittleEndian.PutUint16(fmtChunk[2:], 1)
	binary.LittleEndian.PutUint32(fmtChunk[4:], testRate)
	binary.LittleEndian.PutUint32(fmtChunk[8:], testRate*2)
	binary.LittleEndian.PutUint16(fmtChunk[12:], 2)
	binary.LittleEndian.PutUint16(fmtChunk[14:], 16)
	return wavBytes(fmtChunk, pcm)
}

func TestShortWavIsSentWhole(t *testing.T) {
	wav := pcmWav(50)
	pieces := splitWAV(wav, 100, 10)
	if len(pieces) != 1 || len(pieces[0].Data) != len(wav) {
		t.Fatalf("pieces = %d", len(pieces))
	}
}

func TestUnparseableAudioIsSentWhole(t *testing.T) {
	pieces := splitWAV(make([]byte, 2048), 100, 10)
	if len(pieces) != 1 || len(pieces[0].Data) != 2048 {
		t.Fatalf("pieces = %d", len(pieces))
	}
}

func TestLongWavIsCutInTheQuietSpot(t *testing.T) {
	pieces := splitWAV(pcmWav(250, [2]float64{118, 120}), 150, 10)
	if len(pieces) != 2 {
		t.Fatalf("pieces = %d, want 2", len(pieces))
	}
	if cut := pieces[1].OffsetSec; cut < 118 || cut > 120 {
		t.Errorf("cut at %.2fs, want inside the 118-120s silence", cut)
	}

	var total float64
	for i, p := range pieces {
		l, ok := parseWAV(p.Data)
		if !ok {
			t.Fatalf("piece %d is not a wav", i)
		}
		if got := float64(l.dataLen) / testRate / 2; math.Abs(got-p.DurationSec) > 0.01 || got > 150 {
			t.Errorf("piece %d holds %.2fs, header says %.2fs", i, got, p.DurationSec)
		}
		total += p.DurationSec
	}
	if math.Abs(total-250) > 0.01 {
		t.Errorf("pieces cover %.2fs of 250s", total)
	}
}

func TestEveryPieceStaysUnderTheCap(t *testing.T) {
	for _, p := range splitWAV(pcmWav(1000), 150, 10) {
		if p.DurationSec > 150 {
			t.Errorf("piece at %.0fs is %.2fs long", p.OffsetSec, p.DurationSec)
		}
	}
}

func TestLongRecordingIsTranscribedInPieces(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(call int) (int, string) {
		body, _ := json.Marshal(map[string]any{
			"language": "en", "text": fmt.Sprintf("piece%d", call),
			"words":    []map[string]any{{"word": fmt.Sprintf("piece%d", call), "start": 1.0, "end": 1.5}},
			"segments": []map[string]any{{"start": 1.0, "end": 1.5, "text": fmt.Sprintf("piece%d", call)}},
		})
		return 200, string(body)
	}

	total := (sttMaxPieceSec-sttCutSearchSec)*2 - 100
	quietFrom := total/2 - 30
	wav := pcmWav(total, [2]float64{quietFrom, quietFrom + 2})
	path := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(path, wav, 0o644); err != nil {
		t.Fatal(err)
	}

	c := anchoredClock()
	tr := TranscribeFallback(c, path, STTOptions{Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}}})
	if !tr.OK {
		t.Fatalf("not ok: %s", tr.Reason)
	}
	if m.calls != 2 || len(tr.Segments) != 2 || len(tr.RawSegments) != 2 {
		t.Fatalf("calls=%d segments=%d raw=%d, want 2 each", m.calls, len(tr.Segments), len(tr.RawSegments))
	}

	cutMs := tr.Segments[1].AudioStartMs - 1000
	if cutMs < quietFrom*1000 || cutMs > (quietFrom+2)*1000 {
		t.Errorf("second piece offset %.0fms, want inside the silence", cutMs)
	}
	want, _ := c.FromAudioTime(tr.Segments[1].AudioStartMs / 1000)
	if tr.Segments[1].T != want || tr.RawSegments[1].Start*1000 != tr.Segments[1].AudioStartMs {
		t.Errorf("second piece is not shifted onto the session clock: %+v", tr.Segments[1])
	}
}

func TestFailedPieceNamesItself(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(call int) (int, string) {
		if call == 2 {
			return 400, `{"error":"nope"}`
		}
		return 200, wordResponse
	}
	path := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(path, pcmWav((sttMaxPieceSec-sttCutSearchSec)*2-100), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := TranscribeFallback(anchoredClock(), path, STTOptions{Endpoints: []Endpoint{{URL: m.base()}}})
	if tr.OK || !strings.Contains(tr.Reason, "piece 2/2") {
		t.Fatalf("reason = %q", tr.Reason)
	}
}

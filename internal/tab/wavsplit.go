package tab

import (
	"encoding/binary"
	"math"
)

// STT servers cap the audio they accept per request (speaches: 1800 s), so a
// long session is uploaded as several pieces, each cut at the quietest moment
// near an even division of the recording.
const (
	sttMaxPieceSec  = 1200.0
	sttCutSearchSec = 60.0
	sttCutWindowSec = 0.5
)

type wavPiece struct {
	OffsetSec   float64
	DurationSec float64
	Data        []byte
}

type wavLayout struct {
	fmtChunk   []byte
	dataStart  int
	dataLen    int
	byteRate   int
	blockAlign int
	pcm16      bool
}

func parseWAV(data []byte) (wavLayout, bool) {
	var l wavLayout
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return l, false
	}
	for pos := 12; pos+8 <= len(data); {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4:]))
		body := pos + 8
		switch id {
		case "fmt ":
			if size < 16 || body+size > len(data) {
				return l, false
			}
			l.fmtChunk = data[body : body+size]
			l.byteRate = int(binary.LittleEndian.Uint32(l.fmtChunk[8:]))
			l.blockAlign = int(binary.LittleEndian.Uint16(l.fmtChunk[12:]))
			l.pcm16 = binary.LittleEndian.Uint16(l.fmtChunk[0:]) == 1 &&
				binary.LittleEndian.Uint16(l.fmtChunk[14:]) == 16
		case "data":
			l.dataStart = body
			l.dataLen = size
			if rest := len(data) - body; size > rest {
				l.dataLen = rest
			}
			return l, l.fmtChunk != nil && l.byteRate > 0 && l.blockAlign > 0
		}
		pos = body + size + size%2
	}
	return l, false
}

func splitWAV(data []byte, maxSec, searchSec float64) []wavPiece {
	l, ok := parseWAV(data)
	if !ok {
		return []wavPiece{{Data: data}}
	}
	total := float64(l.dataLen) / float64(l.byteRate)
	if total <= maxSec {
		return []wavPiece{{DurationSec: total, Data: data}}
	}
	if searchSec >= maxSec/2 {
		searchSec = maxSec / 2
	}

	pcm := data[l.dataStart : l.dataStart+l.dataLen]
	align := func(sec float64) int {
		b := int(sec * float64(l.byteRate))
		return b - b%l.blockAlign
	}

	n := int(math.Ceil(total / (maxSec - searchSec)))
	target := total / float64(n)

	var pieces []wavPiece
	from := 0
	for k := 1; k <= n; k++ {
		to := len(pcm) - len(pcm)%l.blockAlign
		if k < n {
			hi := align(target * float64(k))
			to = hi
			if l.pcm16 {
				to = quietestCut(pcm, align(target*float64(k)-searchSec), hi, l)
			}
		}
		pieces = append(pieces, wavPiece{
			OffsetSec:   float64(from) / float64(l.byteRate),
			DurationSec: float64(to-from) / float64(l.byteRate),
			Data:        wavBytes(l.fmtChunk, pcm[from:to]),
		})
		from = to
	}
	return pieces
}

func quietestCut(pcm []byte, lo, hi int, l wavLayout) int {
	window := int(sttCutWindowSec*float64(l.byteRate)) / l.blockAlign * l.blockAlign
	step := window / 5
	step -= step % l.blockAlign
	if window == 0 || step == 0 || hi-lo < window {
		return hi
	}

	best, bestEnergy := hi, math.MaxFloat64
	for start := lo; start+window <= hi; start += step {
		var energy float64
		for i := start; i+1 < start+window; i += 2 {
			energy += math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[i:]))))
		}
		if energy < bestEnergy {
			mid := start + window/2
			best, bestEnergy = mid-mid%l.blockAlign, energy
		}
	}
	return best
}

func wavBytes(fmtChunk, pcm []byte) []byte {
	out := make([]byte, 0, 28+len(fmtChunk)+len(pcm))
	u32 := func(v int) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(v)) }
	out = append(out, "RIFF"...)
	out = append(out, u32(20+len(fmtChunk)+len(pcm))...)
	out = append(out, "WAVEfmt "...)
	out = append(out, u32(len(fmtChunk))...)
	out = append(out, fmtChunk...)
	out = append(out, "data"...)
	out = append(out, u32(len(pcm))...)
	return append(out, pcm...)
}

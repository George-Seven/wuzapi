package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

const audioWaveformBins = 64

// generateAudioWaveform decodes audio to mono PCM and produces one average
// absolute-amplitude level per equal-duration bin. The threshold and curve
// were fit against a native WhatsApp voice note; WhatsApp's transform is not
// documented, so these values remain an empirical approximation.
func generateAudioWaveform(ctx context.Context, sourcePath string) ([]byte, error) {
	cmd := termuxCommand(ctx, "ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file,pipe",
		"-format_whitelist", "matroska,webm,ogg,mov,mp4,m4a,3gp,3g2,mj2,wav,mp3,flac,aac",
		"-i", sourcePath, "-map", "0:a:0", "-vn", "-t", "300",
		"-ac", "1", "-ar", "48000", "-f", "s16le", "-acodec", "pcm_s16le", "pipe:1",
	)
	var diagnostics cappedMediaLog
	cmd.Stderr = &diagnostics
	pcm, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg waveform decode failed: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}

	sampleCount := len(pcm) / 2
	if sampleCount < audioWaveformBins {
		return nil, fmt.Errorf("audio has too few decoded samples for a %d-bin waveform", audioWaveformBins)
	}

	blockSize := sampleCount / audioWaveformBins
	levels := make([]float64, audioWaveformBins)
	maxLevel := 0.0
	for bin := range levels {
		start := bin * blockSize
		end := start + blockSize
		var sumAmplitude float64
		for sampleIndex := start; sampleIndex < end; sampleIndex++ {
			sample := int16(binary.LittleEndian.Uint16(pcm[sampleIndex*2 : sampleIndex*2+2]))
			sumAmplitude += math.Abs(float64(sample))
		}
		levels[bin] = sumAmplitude / float64(blockSize)
		if levels[bin] > maxLevel {
			maxLevel = levels[bin]
		}
	}

	waveform := make([]byte, audioWaveformBins)
	for i, level := range levels {
		if maxLevel == 0 || level/maxLevel <= 0.015 {
			continue
		}
		value := math.Floor(100 * math.Pow(level/maxLevel, 0.32))
		if value > 100 {
			value = 100
		}
		waveform[i] = byte(value)
	}
	return waveform, nil
}

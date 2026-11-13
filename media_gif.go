package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

const (
	gifMaxSeconds   = 6
	gifDefaultSecs  = 6
	gifConvertLimit = 60 * time.Second
)

func stageGIFUpload(parent context.Context, input multipart.File, declaredMIME string, limit int64) (*mediaFile, error) {
	ctx, cancel, err := mediaContext(parent)
	if err != nil {
		return nil, err
	}
	defer cancel()
	store, err := getMediaStore()
	if err != nil {
		return nil, err
	}
	release, err := store.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	media, output, err := store.create("", declaredMIME)
	if err != nil {
		return nil, err
	}
	n, copyErr := io.Copy(output, io.LimitReader(input, limit+1))
	closeErr := output.Close()
	if copyErr != nil {
		media.Close()
		return nil, copyErr
	}
	if closeErr != nil {
		media.Close()
		return nil, closeErr
	}
	if n == 0 || n > limit {
		media.Close()
		return nil, fmt.Errorf("GIF input must be between 1 byte and %d bytes", limit)
	}
	media.Size = n
	media.DeclaredMIME = declaredMIME
	media.MIME, err = media.sniff()
	if err != nil {
		media.Close()
		return nil, err
	}
	return media, nil
}

func convertGIFPlayback(parent context.Context, input *mediaFile, seconds uint32) (*mediaFile, []byte, error) {
	ctx, cancel, err := mediaContext(parent)
	if err != nil {
		return nil, nil, err
	}
	defer cancel()
	ctx, timeoutCancel := context.WithTimeout(ctx, gifConvertLimit)
	defer timeoutCancel()
	store, err := getMediaStore()
	if err != nil {
		return nil, nil, err
	}
	release, err := store.acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	output, outputFile, err := store.create("animation.mp4", "video/mp4")
	if err != nil {
		return nil, nil, err
	}
	if err = outputFile.Close(); err != nil {
		output.Close()
		return nil, nil, err
	}
	cmd := termuxCommand(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y",
		"-protocol_whitelist", "file,pipe",
		"-format_whitelist", "gif,mov,mp4,m4a,3gp,3g2,mj2,matroska,webm",
		"-i", input.Path, "-map", "0:v:0", "-an", "-t", strconv.FormatUint(uint64(seconds), 10),
		"-vf", "fps=15,scale=512:512:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "30", "-pix_fmt", "yuv420p",
		"-movflags", "+faststart", "-f", "mp4", output.Path)
	var diagnostics cappedMediaLog
	cmd.Stderr = &diagnostics
	if err = cmd.Run(); err != nil {
		output.Close()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, nil, errors.New("GIF conversion exceeded 60 seconds")
		}
		return nil, nil, fmt.Errorf("could not convert input to WhatsApp GIF video: %s", diagnostics.String())
	}
	if err = output.refresh(); err != nil {
		output.Close()
		return nil, nil, err
	}
	if output.Size == 0 {
		output.Close()
		return nil, nil, errors.New("FFmpeg produced an empty GIF video")
	}

	thumbnailCmd := termuxCommand(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-ss", "0.1", "-i", output.Path,
		"-frames:v", "1", "-vf", "scale=512:512:force_original_aspect_ratio=decrease",
		"-q:v", "4", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
	var thumbnail bytes.Buffer
	var thumbnailDiagnostics cappedMediaLog
	thumbnailCmd.Stdout = &thumbnail
	thumbnailCmd.Stderr = &thumbnailDiagnostics
	if err = thumbnailCmd.Run(); err != nil || thumbnail.Len() == 0 {
		output.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("could not create GIF thumbnail: %s", thumbnailDiagnostics.String())
		}
		return nil, nil, errors.New("FFmpeg produced an empty GIF thumbnail")
	}
	return output, thumbnail.Bytes(), nil
}

func isGIFVideoInput(mimeType string) bool {
	return mimeType == "image/gif" || strings.HasPrefix(mimeType, "video/")
}

func parseGIFSeconds(value string) (uint32, error) {
	if value == "" {
		return gifDefaultSecs, nil
	}
	seconds, err := strconv.ParseUint(value, 10, 32)
	if err != nil || seconds < 1 || seconds > gifMaxSeconds {
		return 0, fmt.Errorf("Seconds must be between 1 and %d", gifMaxSeconds)
	}
	return uint32(seconds), nil
}

func uploadGIFMedia(parent context.Context, client *whatsmeow.Client, media *mediaFile, recipient types.JID) (whatsmeow.UploadResponse, error) {
	if recipient.Server != types.NewsletterServer {
		return uploadMedia(parent, client, media, whatsmeow.MediaVideo)
	}
	ctx, cancel, err := mediaContext(parent)
	if err != nil {
		return whatsmeow.UploadResponse{}, err
	}
	defer cancel()
	store, err := getMediaStore()
	if err != nil {
		return whatsmeow.UploadResponse{}, err
	}
	release, err := store.acquire(ctx)
	if err != nil {
		return whatsmeow.UploadResponse{}, err
	}
	defer release()
	reader, err := media.Open()
	if err != nil {
		return whatsmeow.UploadResponse{}, err
	}
	defer reader.Close()
	return client.UploadNewsletterReader(ctx, reader, whatsmeow.MediaVideo)
}

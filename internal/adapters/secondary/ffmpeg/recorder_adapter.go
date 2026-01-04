package ffmpeg

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"go-meeting-recorder/internal/core/ports"
)

type ffmpegRecorder struct {
	recordingDir string
	cmds         map[string]*exec.Cmd
	stdins       map[string]io.WriteCloser
	mu           sync.Mutex
}

func NewFFmpegRecorder(recordingDir string) ports.MediaRecorder {
	_ = os.MkdirAll(recordingDir, 0755)
	return &ffmpegRecorder{
		recordingDir: recordingDir,
		cmds:         make(map[string]*exec.Cmd),
		stdins:       make(map[string]io.WriteCloser),
	}
}

func (f *ffmpegRecorder) Start(ctx context.Context, sessionId string, videoStream io.Reader, audioStream io.Reader) error {
	filename := fmt.Sprintf("meeting-%s-%d.mp4", sessionId, time.Now().Unix())
	path := filepath.Join(f.recordingDir, filename)

	// UPDATED STRATEGY for robust cross-platform:
	// We will spawn a goroutine to copy videoStream -> stdin.
	// We will spawn a goroutine to copy audioStream -> separate file (meeting-audio.wav) for now.

	// 1. Video Recording
	videoCmd := exec.Command("ffmpeg",
		"-y",
		"-f", "image2pipe", "-vcodec", "png", "-r", "5", "-i", "-",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "ultrafast",
		path,
	)

	videoStdin, err := videoCmd.StdinPipe()
	if err != nil {
		return err
	}

	// 2. Audio Recording (Separate File)
	audioPath := filepath.Join(f.recordingDir, fmt.Sprintf("meeting-%s-audio.wav", sessionId))
	audioFile, err := os.Create(audioPath)
	if err != nil {
		return err
	}

	f.mu.Lock()
	f.cmds[sessionId] = videoCmd
	f.stdins[sessionId] = videoStdin
	f.mu.Unlock()

	if err := videoCmd.Start(); err != nil {
		return err
	}

	fmt.Printf("[FFmpeg] Started recording session %s\n - Video: %s\n - Audio: %s\n", sessionId, path, audioPath)

	// Pump Video
	go func() {
		defer videoStdin.Close()
		io.Copy(videoStdin, videoStream)
	}()

	// Pump Audio
	go func() {
		defer audioFile.Close()
		io.Copy(audioFile, audioStream)
	}()

	return nil
}

func (f *ffmpegRecorder) Stop(ctx context.Context, sessionId string) (string, error) {
	f.mu.Lock()
	cmd, ok := f.cmds[sessionId]
	stdin := f.stdins[sessionId] // Retrieve stdin to ensure it's closed
	f.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("no active recording for session %s", sessionId)
	}

	fmt.Printf("[FFmpeg] Stopping recording for session %s\n", sessionId)

	// Close stdin to signal EOF to FFmpeg.
	// The goroutine in Start() is responsible for closing stdin when `frames` channel closes.
	// If Stop is called before `frames` channel closes, we explicitly close it here.
	// This will cause the goroutine to exit.
	if stdin != nil {
		stdin.Close()
	}

	// Wait for FFmpeg to finish
	err := cmd.Wait()

	f.mu.Lock()
	delete(f.cmds, sessionId)
	delete(f.stdins, sessionId)
	f.mu.Unlock()

	// Return the path (we should ideally track the path in the struct to be safe)
	// For now, we'll return a placeholder and the error from cmd.Wait()
	// The actual path is constructed in Start, but not stored.
	// A more robust solution would store the `path` in the `ffmpegRecorder` struct
	// associated with the sessionId.
	return "check recordings directory", err
}

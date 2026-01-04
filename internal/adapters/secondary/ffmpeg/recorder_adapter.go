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
	audioCmds    map[string]*exec.Cmd // Store audio processes
	stdins       map[string]io.WriteCloser
	mu           sync.Mutex
}

func NewFFmpegRecorder(recordingDir string) ports.MediaRecorder {
	_ = os.MkdirAll(recordingDir, 0755)
	return &ffmpegRecorder{
		recordingDir: recordingDir,
		cmds:         make(map[string]*exec.Cmd),
		audioCmds:    make(map[string]*exec.Cmd),
		stdins:       make(map[string]io.WriteCloser),
	}
}

func (f *ffmpegRecorder) Start(ctx context.Context, sessionId string, videoStream io.Reader, audioStream io.Reader) error {
	filename := fmt.Sprintf("meeting-%s-%d.mp4", sessionId, time.Now().Unix())
	path := filepath.Join(f.recordingDir, filename)

	// 1. Video Recording (From Pipe)
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

	// 2. Audio Recording (PulseAudio System Capture)
	// Records from default output (what the bot "hears")
	audioFilename := fmt.Sprintf("meeting-%s-audio.wav", sessionId)
	audioPath := filepath.Join(f.recordingDir, audioFilename)
	
	audioCmd := exec.Command("ffmpeg",
		"-y",
		"-f", "pulse", "-i", "default",
		"-ac", "2",
		audioPath,
	)

	// Start Audio Process
	if err := audioCmd.Start(); err != nil {
		fmt.Printf("[FFmpeg] Failed to start audio recording: %v\n", err)
		// Proceed with video only if audio fails
	} else {
		fmt.Printf("[FFmpeg] Started audio recording: %s\n", audioPath)
	}

	f.mu.Lock()
	f.cmds[sessionId] = videoCmd
	f.audioCmds[sessionId] = audioCmd
	f.stdins[sessionId] = videoStdin
	f.mu.Unlock()

	if err := videoCmd.Start(); err != nil {
		return err
	}

	fmt.Printf("[FFmpeg] Started video recording session %s to %s\n", sessionId, path)

	// Pump Video
	if videoStream != nil {
		go func() {
			defer videoStdin.Close()
			io.Copy(videoStdin, videoStream)
		}()
	} else {
		videoStdin.Close()
	}

	return nil
}

func (f *ffmpegRecorder) Stop(ctx context.Context, sessionId string) (string, error) {
	f.mu.Lock()
	videoCmd, ok := f.cmds[sessionId]
	audioCmd := f.audioCmds[sessionId]
	stdin := f.stdins[sessionId]
	f.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("no active recording for session %s", sessionId)
	}

	fmt.Printf("[FFmpeg] Stopping recording for session %s\n", sessionId)

	// Stop Video: Close stdin to signal EOF
	if stdin != nil {
		stdin.Close()
	}
	// Wait for video finish
	err := videoCmd.Wait()

	// Stop Audio: Process must be killed (SIGTERM)
	if audioCmd != nil && audioCmd.Process != nil {
		fmt.Println("[FFmpeg] Stopping audio process...")
		_ = audioCmd.Process.Signal(os.Interrupt)
		// Give it a moment to finalize file headers
		done := make(chan error)
		go func() { done <- audioCmd.Wait() }()
		
		select {
		case <-done:
			// Process exited clean-ish
		case <-time.After(2 * time.Second):
			// Force kill if stuck
			_ = audioCmd.Process.Kill()
		}
	}

	f.mu.Lock()
	delete(f.cmds, sessionId)
	delete(f.audioCmds, sessionId)
	delete(f.stdins, sessionId)
	f.mu.Unlock()

	return "check recordings directory", err
}

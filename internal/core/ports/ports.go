package ports

import (
	"context"
	"go-meeting-recorder/internal/core/domain"
	"io"
)

// Primary Port (Driving) - implemented by Service
type RecordingService interface {
	StartRecording(ctx context.Context, meetingUrl, participantName string) (*domain.MeetingSession, error)
	StopRecording(ctx context.Context, sessionId string) (*domain.MeetingSession, error)
	GetSessionPlatform(ctx context.Context, sessionId string) (*domain.MeetingSession, error)
}

// Secondary Port (Driven) - implemented by Adapters
type BrowserAutomator interface {
	JoinMeeting(ctx context.Context, session *domain.MeetingSession) error
	StopMeeting(ctx context.Context, sessionId string) error
	GetSnapshot(ctx context.Context, sessionId string) ([]byte, error)
	// GetMeetingStreams returns streams for audio and video
	GetMeetingStreams(ctx context.Context, sessionId string) (videoStream io.Reader, audioStream io.Reader, err error)
}

// Secondary Port (Driven)
type MediaRecorder interface {
	Start(ctx context.Context, sessionId string, videoStream io.Reader, audioStream io.Reader) error
	Stop(ctx context.Context, sessionId string) (string, error)
}

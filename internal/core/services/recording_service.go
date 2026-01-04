package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go-meeting-recorder/internal/core/domain"
	"go-meeting-recorder/internal/core/ports"

	"github.com/google/uuid"
)

type recordingService struct {
	sessions      map[string]*domain.MeetingSession
	mu            sync.RWMutex
	automator     ports.BrowserAutomator
	mediaRecorder ports.MediaRecorder
}

func NewRecordingService(automator ports.BrowserAutomator, mediaRecorder ports.MediaRecorder) ports.RecordingService {
	return &recordingService{
		sessions:      make(map[string]*domain.MeetingSession),
		automator:     automator,
		mediaRecorder: mediaRecorder,
	}
}

func (s *recordingService) StartRecording(ctx context.Context, meetingUrl, participantName string) (*domain.MeetingSession, error) {
	id := uuid.New().String()
	session := &domain.MeetingSession{
		ID:              id,
		MeetingURL:      meetingUrl,
		ParticipantName: participantName,
		Status:          domain.StatusInitializing,
		StartTime:       nil,
	}

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()

	// Launch async process to join and record
	go func() {
		// Create a background context for the long-running task
		bgCtx := context.Background()

		// 1. Join Meeting
		s.updateStatus(id, domain.StatusJoining)
		err := s.automator.JoinMeeting(bgCtx, session)
		if err != nil {
			s.updateError(id, fmt.Sprintf("Failed to join: %v", err))
			return
		}

		// Start Recording
		s.updateStatus(id, domain.StatusRecording)
		now := time.Now()
		session.StartTime = &now

		// Start recording streams
		go func() {
			video, audio, err := s.automator.GetMeetingStreams(bgCtx, id)
			if err != nil {
				s.updateError(id, fmt.Sprintf("Failed to get streams: %v", err))
				return
			}

			if err := s.mediaRecorder.Start(bgCtx, id, video, audio); err != nil {
				s.updateError(id, fmt.Sprintf("Recorder failed: %v", err))
			}
		}()
	}()

	return session, nil
}

func (s *recordingService) StopRecording(ctx context.Context, sessionId string) (*domain.MeetingSession, error) {
	s.mu.RLock()
	session, exists := s.sessions[sessionId]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("session not found")
	}

	if session.Status != domain.StatusRecording {
		return session, nil
	}

	s.updateStatus(sessionId, domain.StatusStopping)

	// Stop recorder
	path, err := s.mediaRecorder.Stop(ctx, sessionId)
	if err != nil {
		s.updateError(sessionId, fmt.Sprintf("Failed to stop recorder: %v", err))
		return session, err
	}
	session.FilePath = path

	// Stop browser
	err = s.automator.StopMeeting(ctx, sessionId)
	if err != nil {
		// logging warning usually
	}

	now := time.Now()
	session.EndTime = &now
	session.CalculateDuration()
	s.updateStatus(sessionId, domain.StatusStopped)

	return session, nil
}

func (s *recordingService) GetSessionPlatform(ctx context.Context, sessionId string) (*domain.MeetingSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, exists := s.sessions[sessionId]
	if !exists {
		return nil, fmt.Errorf("session not found")
	}

	// Recalculate duration if ongoing
	if session.Status == domain.StatusRecording && session.StartTime != nil {
		duration := time.Since(*session.StartTime)
		session.Duration = duration.String()
	}

	return session, nil
}

func (s *recordingService) updateStatus(id string, status domain.SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[id]; ok {
		session.Status = status
	}
}

func (s *recordingService) updateError(id string, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.sessions[id]; ok {
		session.Status = domain.StatusError
		session.Error = msg
	}
}

package domain

import (
	"time"
)

type SessionStatus string

const (
	StatusInitializing SessionStatus = "initializing"
	StatusJoining      SessionStatus = "joining"
	StatusRecording    SessionStatus = "recording"
	StatusStopping     SessionStatus = "stopping"
	StatusStopped      SessionStatus = "stopped"
	StatusError        SessionStatus = "error"
)

type MeetingSession struct {
	ID              string        `json:"sessionId"`
	MeetingURL      string        `json:"meetingUrl"`
	ParticipantName string        `json:"participantName"`
	Status          SessionStatus `json:"status"`
	StartTime       *time.Time    `json:"startTime,omitempty"`
	EndTime         *time.Time    `json:"endTime,omitempty"`
	FilePath        string        `json:"filePath,omitempty"`
	Duration        string        `json:"duration,omitempty"` // Formatted duration
	Error           string        `json:"error,omitempty"`
}

func (s *MeetingSession) CalculateDuration() {
	if s.StartTime != nil && s.EndTime != nil {
		duration := s.EndTime.Sub(*s.StartTime)
		s.Duration = duration.String() // Simplified, can be formatted nicely
	}
}

package http

import (
	"encoding/json"
	"net/http"

	"go-meeting-recorder/internal/core/ports"
)

type Handler struct {
	service ports.RecordingService
}

func NewHandler(service ports.RecordingService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /meetings/start", h.startRecording)
	mux.HandleFunc("POST /meetings/stop/{sessionId}", h.stopRecording)
	mux.HandleFunc("GET /meetings/status/{sessionId}", h.getStatus)
}

type startRequest struct {
	MeetingURL      string `json:"meetingUrl"`
	ParticipantName string `json:"participantName"`
}

func (h *Handler) startRecording(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	session, err := h.service.StartRecording(r.Context(), req.MeetingURL, req.ParticipantName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

func (h *Handler) stopRecording(w http.ResponseWriter, r *http.Request) {
	sessionId := r.PathValue("sessionId")
	if sessionId == "" {
		http.Error(w, "sessionId is required", http.StatusBadRequest)
		return
	}

	session, err := h.service.StopRecording(r.Context(), sessionId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

func (h *Handler) getStatus(w http.ResponseWriter, r *http.Request) {
	sessionId := r.PathValue("sessionId")
	if sessionId == "" {
		http.Error(w, "sessionId is required", http.StatusBadRequest)
		return
	}

	session, err := h.service.GetSessionPlatform(r.Context(), sessionId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

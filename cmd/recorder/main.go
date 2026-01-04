package main

import (
	"log"
	"net/http"

	primaryHTTP "go-meeting-recorder/internal/adapters/primary/http"
	"go-meeting-recorder/internal/adapters/secondary/ffmpeg"
	"go-meeting-recorder/internal/adapters/secondary/rod"
	"go-meeting-recorder/internal/core/services"
)

func main() {
	// Initialize Adapters
	rodAdapter := rod.NewRodAutomator()
	ffmpegAdapter := ffmpeg.NewFFmpegRecorder("./recordings")

	// Initialize Service (Core)
	recordingService := services.NewRecordingService(rodAdapter, ffmpegAdapter)

	// Initialize Driving Adapter (HTTP)
	httpHandler := primaryHTTP.NewHandler(recordingService)

	// Setup Router (Go 1.22+ ServeMux)
	mux := http.NewServeMux()
	httpHandler.RegisterRoutes(mux)

	log.Println("Starting server on :8081")
	if err := http.ListenAndServe(":8081", mux); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

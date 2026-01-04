#!/bin/bash
set -e

echo "Starting PulseAudio..."
# Start PulseAudio
pulseaudio --start --exit-idle-time=-1

# Wait for PA to start (max 10 seconds)
for i in {1..10}; do
    if pactl info > /dev/null 2>&1; then
        echo "PulseAudio started successfully."
        break
    fi
    echo "Waiting for PulseAudio... ($i)"
    sleep 1
done

if ! pactl info > /dev/null 2>&1; then
    echo "ERROR: PulseAudio failed to start."
    # Log the output of pulseaudio for debugging
    pulseaudio --start --log-level=debug || true
    # Don't exit yet, maybe app can run without audio
fi

# Create a virtual null sink
echo "Loading virtual sink..."
pactl load-module module-null-sink sink_name=VirtualSink sink_properties=device.description="Virtual_Sink" || echo "Failed to load null sink"

# Set it as the default sink for output
pactl set-default-sink VirtualSink || echo "Failed to set default sink"

# Set the monitor as default source
pactl set-default-source VirtualSink.monitor || echo "Failed to set default source"

echo "PulseAudio initialized with VirtualSink."

# Run the application
exec "$@"


# Build Stage
FROM golang:1.23 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Static build not strictly required as we use Ubuntu, but good practice
RUN CGO_ENABLED=0 GOOS=linux go build -o recorder cmd/recorder/main.go

# Runtime Stage
FROM ubuntu:22.04

# Install runtime dependencies
# chrome-stable, ffmpeg, pulseaudio, dumb-init, certificates
RUN apt-get update && apt-get install -y \
    wget \
    gnupg \
    ca-certificates \
    ffmpeg \
    pulseaudio \
    dumb-init \
    --no-install-recommends \
    && wget -q -O - https://dl-ssl.google.com/linux/linux_signing_key.pub | apt-key add - \
    && echo "deb [arch=amd64] http://dl.google.com/linux/chrome/deb/ stable main" >> /etc/apt/sources.list.d/google.list \
    && apt-get update \
    && apt-get install -y google-chrome-stable \
    && rm -rf /var/lib/apt/lists/*

# Create a non-root user
RUN useradd -m -s /bin/bash recorder

WORKDIR /home/recorder/app

# Copy binary
COPY --from=builder /app/recorder .
COPY --from=builder /app/entrypoint.sh .

# Setup permissions
RUN chown -R recorder:recorder /home/recorder
RUN chmod +x entrypoint.sh

# Create recordings dir
RUN mkdir recordings && chown recorder:recorder recordings

USER recorder

# Expose port
EXPOSE 8081

# Entrypoint
ENTRYPOINT ["/usr/bin/dumb-init", "--", "./entrypoint.sh"]
CMD ["./recorder"]

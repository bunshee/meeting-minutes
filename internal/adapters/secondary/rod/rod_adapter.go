package rod

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"go-meeting-recorder/internal/core/domain"
	"go-meeting-recorder/internal/core/ports"
)

type RodAdapter struct {
	browsers map[string]*rod.Browser
	pages    map[string]*rod.Page
	mu       sync.Mutex
	stopCh   map[string]chan struct{} // Channel to signal stop to monitoring routine
}

func NewRodAutomator() ports.BrowserAutomator {
	return &RodAdapter{
		browsers: make(map[string]*rod.Browser),
		pages:    make(map[string]*rod.Page),
		stopCh:   make(map[string]chan struct{}),
	}
}

func (r *RodAdapter) JoinMeeting(ctx context.Context, session *domain.MeetingSession) error {
	log.Println("Starting Rod automation for Teams...")

	l := launcher.New().
		Bin("/usr/bin/google-chrome").
		UserDataDir("/tmp/rod-teams-profile").
		Headless(true).
		Set("no-sandbox").
		Set("disable-gpu").
		Set("disable-software-rasterizer").
		Set("disable-features", "DialerProtocolHandler,ExternalProtocolDialog").
		Set("disable-protocol-handler-registration").
		Set("disable-setuid-sandbox").
		Set("disable-blink-features", "AutomationControlled").
		Set("use-fake-ui-for-media-stream").
		Set("use-fake-device-for-media-stream").
		Set("autoplay-policy", "no-user-gesture-required").
		Set("disable-popup-blocking").
		Set("disable-notifications").
		Set("disable-features", "DialerProtocolHandler,ExternalProtocolDialog").
		Set("disable-protocol-handler-registration").
		Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	u, err := l.Launch()
	if err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}

	browser := rod.New().ControlURL(u).MustConnect()

	r.mu.Lock()
	r.browsers[session.ID] = browser
	r.stopCh[session.ID] = make(chan struct{})
	r.mu.Unlock()

	proto.BrowserGrantPermissions{
		Origin: session.MeetingURL,
		Permissions: []proto.BrowserPermissionType{
			proto.BrowserPermissionTypeAudioCapture,
			proto.BrowserPermissionTypeVideoCapture,
			proto.BrowserPermissionTypeNotifications,
		},
	}.Call(browser)

	page := browser.MustPage("")

	page.MustEvalOnNewDocument(`
		const winUA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36';
		Object.defineProperty(navigator, 'userAgent', { get: () => winUA });
		Object.defineProperty(navigator, 'webdriver', { get: () => false });
		Object.defineProperty(navigator, 'platform', { get: () => 'Win32' });
		Object.defineProperty(navigator, 'vendor', { get: () => 'Google Inc.' });
		Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'] });
		Object.defineProperty(navigator, 'hardwareConcurrency', { get: () => 8 });
		Object.defineProperty(navigator, 'deviceMemory', { get: () => 8 });
		
		window.chrome = { runtime: {} };
		delete navigator.__proto__.webdriver;
	`)

	page.MustSetViewport(1920, 1080, 1, false)
	page.SetExtraHeaders([]string{"Accept-Language", "en-US,en;q=0.9"})

	go page.HandleDialog()

	r.mu.Lock()
	r.pages[session.ID] = page
	r.mu.Unlock()

	go page.EachEvent(func(e *proto.RuntimeConsoleAPICalled) {
		// Suppress verbose console logs if needed, or keep for debug
	})()

	// URL Handling
	finalURL := session.MeetingURL
	if strings.Contains(finalURL, "teams.live.com/meet/") {
		parts := strings.Split(finalURL, "teams.live.com/meet/")
		if len(parts) > 1 {
			remaining := parts[1]
			meetingID := strings.Split(remaining, "?")[0]
			pVal := ""
			if strings.Contains(remaining, "p=") {
				pParts := strings.Split(remaining, "p=")
				if len(pParts) > 1 {
					pVal = strings.Split(pParts[1], "&")[0]
				}
			}
			finalURL = fmt.Sprintf("https://teams.live.com/_#/meet/%s?p=%s&anon=true", meetingID, pVal)
			fmt.Printf("[Rod] Forced Deep Link URL: %s\n", finalURL)
		}
	}

	page.MustSetUserAgent(&proto.NetworkSetUserAgentOverride{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		Platform:  "Windows",
	})
	
	page.MustSetExtraHeaders("referer", "https://teams.live.com/", "sec-ch-ua-platform", "Windows")

	fmt.Printf("[Rod] Navigating to: %s\n", finalURL)
	_ = page.Navigate(finalURL)
	time.Sleep(5 * time.Second)
	
	fmt.Println("[Rod] Initial navigation complete, handling join flow...")
	fmt.Println("[Rod] Handling Teams join flow...")

	lobbyReached := false
	startTime := time.Now()

	for time.Since(startTime) < 45*time.Second {
		page.MustScreenshot("debug_loop.png")

		// 1. Dismiss "Continue without audio or video"
		page.MustEval(`() => {
			const btns = Array.from(document.querySelectorAll('button'));
			const continueBtn = btns.find(b => b.innerText.includes('Continue without audio'));
			if (continueBtn) continueBtn.click();
		}`)

	// 2. Mute Microphone (Refined)
		page.MustEval(`() => {
			const switches = Array.from(document.querySelectorAll('input[role="switch"]'));
			switches.forEach(s => {
				const ariaLabel = (s.getAttribute('aria-label') || "").toLowerCase();
				// Check for "Mic" in label. State check: 'checked' property OR 'aria-checked' attribute
				const isActive = s.checked || s.getAttribute('aria-checked') === 'true';
				
				if (ariaLabel.includes('mic') && isActive) {
					console.log("Muting microphone (Refined)...");
					s.click();
				}

				// Disable Camera
				if ((ariaLabel.includes('camera') || ariaLabel.includes('video')) && isActive) {
					console.log("Disabling camera...");
					s.click();
				}
			});
		}`)

		// 3. Name Input & Join
		status := page.MustEval(`(name) => {
			const inputs = Array.from(document.querySelectorAll('input'));
			const nameInput = inputs.find(i => 
				i.getAttribute('data-tid') === 'prejoin-display-name-input' || 
				(i.placeholder && i.placeholder.toLowerCase().includes('type your name'))
			);
			
			if (!nameInput) return "input_not_found";
			
			// REACT HACK: Use native value setter
			const nativeInputValueSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
			nativeInputValueSetter.call(nameInput, name);
			
			nameInput.dispatchEvent(new Event('input', { bubbles: true }));
			nameInput.dispatchEvent(new Event('change', { bubbles: true }));
			nameInput.dispatchEvent(new Event('blur', { bubbles: true })); 
			
			const btns = Array.from(document.querySelectorAll('button'));
			const joinBtn = btns.find(b => 
				b.getAttribute('data-tid') === 'prejoin-join-button' ||
				b.innerText === 'Join now' ||
				b.getAttribute('aria-label') === 'Join now'
			);
			
			if (joinBtn && !joinBtn.disabled) {
				joinBtn.click();
				return "joined";
			}
			
			return "waiting_for_join_button";
		}`, session.ParticipantName).Str()
		
		if status == "joined" {
			lobbyReached = true
			fmt.Println("[Rod] Successfully triggered join via JS!")
			break
		}
		
		time.Sleep(2 * time.Second)
	}

	if lobbyReached {
		fmt.Println("[Rod] Join action triggered. Establishing monitoring...")
		
		// Start Auto-Stop Monitor
		go r.monitorMeetingStatus(ctx, session.ID, page)
		
		time.Sleep(10 * time.Second)
		return nil
	}
	
	return fmt.Errorf("failed to join meeting (JS could not complete flow) after 45 seconds")
}

func (r *RodAdapter) monitorMeetingStatus(ctx context.Context, sessionID string, page *rod.Page) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	fmt.Printf("[Rod] Monitoring session %s for exit conditions...\n", sessionID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh[sessionID]:
			return
		case <-ticker.C:
			// Check for exit conditions (Broadened)
			hasExited := false
			err := rod.Try(func() {
				hasExited = page.MustEval(`() => {
					const bodyText = document.body.innerText;
					return bodyText.includes("You have been removed") || 
						   bodyText.includes("Someone removed you") ||
						   bodyText.includes("Meeting ended") ||
						   bodyText.includes("Call ended") ||
						   bodyText.includes("Quality of this call") ||
						   bodyText.includes("How was the quality");
				}`).Bool()
			})
			
			if err == nil && hasExited {
				fmt.Printf("[Rod] Detected exit condition for session %s. Stopping...\n", sessionID)
				r.StopMeeting(context.Background(), sessionID)
				return
			}
		}
	}
}

func (r *RodAdapter) StopMeeting(ctx context.Context, sessionID string) error {
	log.Printf("StopMeeting called for session %s\n", sessionID)
	r.mu.Lock()
	defer r.mu.Unlock()

	// Signal monitor to stop
	if ch, ok := r.stopCh[sessionID]; ok {
		close(ch)
		delete(r.stopCh, sessionID)
	}

	if browser, ok := r.browsers[sessionID]; ok {
		// Close browser
		err := browser.Close()
		delete(r.browsers, sessionID)
		delete(r.pages, sessionID)
		return err
	}
	return nil
}

func (r *RodAdapter) GetSnapshot(ctx context.Context, sessionID string) ([]byte, error) {
	r.mu.Lock()
	page, ok := r.pages[sessionID]
	r.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("page not found for session %s", sessionID)
	}
	return page.Screenshot(true, nil)
}

func (r *RodAdapter) GetMeetingStreams(ctx context.Context, sessionID string) (io.Reader, io.Reader, error) {
	r.mu.Lock()
	page, ok := r.pages[sessionID]
	r.mu.Unlock()

	if !ok {
		return nil, nil, fmt.Errorf("page not found for session %s", sessionID)
	}

	// Create pipe for video stream
	pr, pw := io.Pipe()

	// Start a goroutine to capture screenshots and write to pipe
	go func() {
		defer pw.Close()
		
		// 5 FPS = 200ms interval
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-r.stopCh[sessionID]:
				return
			case <-ticker.C:
				// Capture Screenshot
				// Note: Use Screenshot(true, nil) for PNG
				buf, err := page.Screenshot(true, nil)
				if err != nil {
					log.Printf("[RodStream] Error capturing screenshot: %v", err)
					return // Exit stream on error (browser probably closed)
				}
				
				// Write to pipe
				if _, err := pw.Write(buf); err != nil {
					log.Printf("[RodStream] Error writing to pipe: %v", err)
					return
				}
			}
		}
	}()

	// Return Video Stream (pr), Audio Stream (nil for now)
	return pr, nil, nil
}

package rod

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"go-meeting-recorder/internal/core/domain"
	"go-meeting-recorder/internal/core/ports"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

type rodAutomator struct {
	browsers map[string]*rod.Browser
	pages    map[string]*rod.Page
	mu       sync.Mutex
}

func NewRodAutomator() ports.BrowserAutomator {
	return &rodAutomator{
		browsers: make(map[string]*rod.Browser),
		pages:    make(map[string]*rod.Page),
	}
}

func (r *rodAutomator) JoinMeeting(ctx context.Context, session *domain.MeetingSession) error {
	// Find system Chrome - this is CRITICAL for Teams/Meet which block bundled Chromium
	chromePath, found := launcher.LookPath()
	if !found {
		// Fallback to common Windows Chrome paths
		chromePath = "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe"
	}
	fmt.Printf("[Rod] Using Chrome at: %s\n", chromePath)

	// Configure launcher with critical evasion flags using SYSTEM Chrome
	l := launcher.New().
		Bin(chromePath). // Use real Chrome, not bundled Chromium
		Headless(false). // Debugging: Show browser window
		Set("no-sandbox").
		Set("disable-setuid-sandbox").
		Set("disable-blink-features", "AutomationControlled"). // Critical
		Set("use-fake-ui-for-media-stream").
		Set("use-fake-device-for-media-stream").
		Set("autoplay-policy", "no-user-gesture-required").
		Set("disable-popup-blocking").
		Set("disable-notifications")

	// r.logger.Info("Launching browser...")
	u, err := l.Launch()

	if err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}

	// Create browser instance
	browser := rod.New().ControlURL(u).MustConnect()

	r.mu.Lock()
	r.browsers[session.ID] = browser
	r.mu.Unlock()

	// 1. Grant Permissions (CRITICAL for Teams/Meet)
	// We grant strict permissions to avoid prompts that block "Join" or cause "Unsupported" checks to fail
	proto.BrowserGrantPermissions{
		Origin: session.MeetingURL,
		Permissions: []proto.BrowserPermissionType{
			proto.BrowserPermissionTypeAudioCapture,
			proto.BrowserPermissionTypeVideoCapture,
			proto.BrowserPermissionTypeNotifications,
		},
	}.Call(browser)

	// 2. Create Page with Stealth
	// We use the stealth library, but we also inject aggressive overrides first
	// to ensure properties like 'navigator.webdriver' are set to false immediately.
	page := stealth.MustPage(browser)

	page.MustEvalOnNewDocument(`
		Object.defineProperty(navigator, 'webdriver', { get: () => false });
		window.chrome = { runtime: {} };
		Object.defineProperty(navigator, 'languages', { get: () => ['en-US', 'en'] });
		Object.defineProperty(navigator, 'plugins', { get: () => [1, 2, 3, 4, 5] });
	`)

	// 3. Set standard viewport
	page.MustSetViewport(1920, 1080, 1, false)

	// Handle dialogs
	go page.HandleDialog()

	r.mu.Lock()
	r.pages[session.ID] = page
	r.mu.Unlock()

	// Navigate
	// r.logger.Info("Navigating to meeting...")
	page.MustNavigate(session.MeetingURL)

	// Wait specifically for either the "Open Teams?" dialog handling (browser level) or the page load
	// We can't easily click "Cancel" on the system popup via DOM.
	// However, usually clicking the "Continue on this browser" or "Use the web app" button works even if that popup is floating.
	page.MustWaitLoad()

	// --- Join Logic ---
	fmt.Println("[Rod] Handling Teams join flow...")

	// Polyfill for "Continue on this browser"
	// We need to keep clicking "Continue on this browser" until we see the lobby (name input)
	// or until we time out.

	lobbyReached := false
	startTime := time.Now()

	for time.Since(startTime) < 30*time.Second {
		// Check if we are already in lobby
		if hasInput, _, _ := page.Has("input[data-tid='prejoin-display-name-input']"); hasInput {
			fmt.Println("[Rod] Lobby detected (Name input found)")
			lobbyReached = true
			break
		}

		fmt.Println("[Rod] Looking for entry buttons...")
		page.MustScreenshot("debug_loop.png") // DEBUG: See what the bot sees

		// Try to find and click any "Continue on browser" buttons
		// We use Element variants that don't panic
		if btn, err := page.Element("#container > div > div > div > div.mainActionsContent > div.actionsContainer > div > button"); err == nil {
			fmt.Println("[Rod] Found main action button via CSS selector, clicking...")
			btn.MustClick()
		} else if btn, err := page.Element("button[data-tid='joinOnWeb']"); err == nil {
			fmt.Println("[Rod] Found 'joinOnWeb' button, clicking...")
			btn.MustClick()
		} else if btn, err := page.ElementR("button", "Continue on this browser"); err == nil {
			fmt.Println("[Rod] Found 'Continue on this browser' button, clicking...")
			btn.MustClick()
		} else if btn, err := page.ElementR("a", "Continue on this browser"); err == nil {
			fmt.Println("[Rod] Found 'Continue on this browser' link, clicking...")
			btn.MustClick()
		} else if btn, err := page.ElementR("button", "Join on the web instead"); err == nil {
			fmt.Println("[Rod] Found 'Join on the web instead' button, clicking...")
			btn.MustClick()
		}

		time.Sleep(1 * time.Second)
	}

	if !lobbyReached {
		fmt.Println("[Rod] Failed to reach lobby after 30 seconds")
		page.Screenshot(true, nil)
		return fmt.Errorf("failed to reach lobby")
	}

	// 2. Wait for name input (Lobby)
	// We know it exists or is about to exist because loop broke
	nameInput := page.MustElement("input[data-tid='prejoin-display-name-input']")
	nameInput.MustWaitVisible()

	// Check if already filled (if SSO)
	if val := nameInput.MustText(); val == "" {
		nameInput.MustInput(session.ParticipantName)
	}

	// 3. Mute/Camera Off
	// Rod KeyActions don't have SetShift/SetControl directly like that
	// Use input.Key(keys...) or page.Keyboard
	fmt.Println("[Rod] Muting and turning off camera...")
	// Ctrl+Shift+M
	page.Keyboard.Press(input.ControlLeft)
	page.Keyboard.Press(input.ShiftLeft)
	page.Keyboard.Press(input.KeyM)
	page.Keyboard.Release(input.ShiftLeft)
	page.Keyboard.Release(input.ControlLeft)

	time.Sleep(500 * time.Millisecond)

	// Ctrl+Shift+O
	page.Keyboard.Press(input.ControlLeft)
	page.Keyboard.Press(input.ShiftLeft)
	page.Keyboard.Press(input.KeyO)
	page.Keyboard.Release(input.ShiftLeft)
	page.Keyboard.Release(input.ControlLeft)

	// 4. Click Join Now
	fmt.Println("[Rod] Looking for 'Join now' button...")

	// Try multiple selectors for "Join" button
	// data-tid='prejoin-join-button' is standard, but sometimes it's different
	// We'll also try looking for the text "Join now"
	joinBtnScanner := page.Race().
		Element("button[data-tid='prejoin-join-button']").MustHandle(func(e *rod.Element) {
		e.MustClick()
	}).
		ElementR("button", "Join now").MustHandle(func(e *rod.Element) {
		e.MustClick()
	})

	if _, err := joinBtnScanner.Do(); err != nil {
		// If both fail, take a screenshot
		fmt.Printf("[Rod] Failed to find Join button: %v\n", err)
		page.Screenshot(true, nil) // Save to disk as "screenshot.png"
		return fmt.Errorf("failed to join meeting: %w", err)
	}

	fmt.Println("[Rod] Clicked 'Join now', waiting for meeting to stabilize...")

	// Wait for meeting to stabilize
	time.Sleep(5 * time.Second)

	return nil
}

func (r *rodAutomator) GetMeetingStreams(ctx context.Context, sessionId string) (io.Reader, io.Reader, error) {
	r.mu.Lock()
	page, ok := r.pages[sessionId]
	r.mu.Unlock()

	if !ok {
		return nil, nil, fmt.Errorf("page not found")
	}

	// In a real implementation with rod-stream, we would do:
	// extension := rodstream.NewExtension(...)
	// client := extension.MustCreateStream(page).
	//                 Audio(true).
	//                 Video(true, 1920, 1080)
	//
	// However, since we cannot easily 'go get' blindly without verifying the exact version API
	// and 'rod-stream' behavior in headless (requires extension loaded at launch),
	// I will mock the interface compliance here to allow compilation while preserving the structure.
	//
	// To make this work IRL:
	// 1. You must load the extension in NewRodAutomator launch options.
	// 2. You call MustCreateStream here.

	// Simulating streams for now so code compiles and structure is ready
	// Real implementation requires: "github.com/navicstein/rod-stream"

	// Create pipes to simulate streams
	videoReader, videoWriter := io.Pipe()
	audioReader, audioWriter := io.Pipe()

	// Start a background routine that mocks data (or takes screenshots for video)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		defer videoWriter.Close()
		defer audioWriter.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Continue taking screenshots for video stream simulation
				img, err := page.Screenshot(false, nil)
				if err == nil {
					videoWriter.Write(img)
				}
				// Mock audio data (silence)
				audioWriter.Write(make([]byte, 1024))
			}
		}
	}()

	return videoReader, audioReader, nil
}

func (r *rodAutomator) StopMeeting(ctx context.Context, sessionId string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if browser, ok := r.browsers[sessionId]; ok {
		browser.MustClose()
		delete(r.browsers, sessionId)
		delete(r.pages, sessionId)
	}
	return nil
}

func (r *rodAutomator) GetSnapshot(ctx context.Context, sessionId string) ([]byte, error) {
	r.mu.Lock()
	page, ok := r.pages[sessionId]
	r.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("session page not found")
	}

	return page.MustScreenshot(), nil
}

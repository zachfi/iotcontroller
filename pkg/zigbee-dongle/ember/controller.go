package ember

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/zachfi/iotcontroller/pkg/zigbee-dongle/types"
)

var ErrTimeout = errors.New("command timed out")

type handlerResult struct {
	frame *EZSPFrame
	err   error
}

type Handler struct {
	results chan handlerResult
	timer   *time.Timer
}

func newHandler() *Handler {
	return &Handler{
		results: make(chan handlerResult, 1),
	}
}

func (h *Handler) fail() {
	h.results <- handlerResult{nil, ErrTimeout}
}

func (h *Handler) fulfill(frame *EZSPFrame) {
	h.results <- handlerResult{frame, nil}
	if h.timer != nil {
		h.timer.Stop()
	}
}

func (h *Handler) Receive() (*EZSPFrame, error) {
	result := <-h.results
	return result.frame, result.err
}

type Settings struct {
	Port               string
	BaudRate           int
	DisableFlowControl bool
	LogCommands        bool
	LogErrors          bool
}

type Controller struct {
	port       *Port
	settings   *Settings
	handlers   map[uint8]*Handler // Map sequence number to handler
	handlerMux sync.Mutex
	done       chan struct{}
	readLoopWg sync.WaitGroup // Wait group for read loop goroutine

	// ASH protocol state
	rxSeq uint8 // Receive sequence number (what we expect next)
	txSeq uint8 // Transmit sequence number (what we're sending)

	// Stack status callback channel (for waiting for NETWORK_UP after FORM_NETWORK)
	stackStatusCh chan byte
}

func NewController(settings Settings) (*Controller, error) {
	port, err := NewPort(settings.Port, settings.BaudRate, settings.DisableFlowControl)
	if err != nil {
		return nil, err
	}

	return &Controller{
		port:          port,
		settings:      &settings,
		handlers:      make(map[uint8]*Handler),
		done:          make(chan struct{}),
		rxSeq:         0,
		txSeq:         0,
		stackStatusCh: make(chan byte, 10), // Buffered to avoid blocking
	}, nil
}

func (c *Controller) Close() error {
	if c.settings.LogCommands {
		fmt.Printf("[ember] Close() called\n")
		os.Stdout.Sync()
	}

	// Signal that we're closing
	select {
	case <-c.done:
		// Already closed
		if c.settings.LogCommands {
			fmt.Printf("[ember] Already closed\n")
			os.Stdout.Sync()
		}
		return nil
	default:
		close(c.done)
	}

	// Fail all pending handlers
	c.handlerMux.Lock()
	for _, handler := range c.handlers {
		handler.fail()
	}
	c.handlers = make(map[uint8]*Handler)
	c.handlerMux.Unlock()

	// Close the port - use a goroutine with timeout to prevent blocking
	// The read loop should have already exited when context was cancelled
	if c.settings.LogCommands {
		fmt.Printf("[ember] Closing port (non-blocking)...\n")
		os.Stdout.Sync()
	}

	portCloseDone := make(chan error, 1)
	go func() {
		portCloseDone <- c.port.Close()
	}()

	// Don't wait long - if port close blocks, we'll timeout and continue
	select {
	case err := <-portCloseDone:
		if err != nil {
			if c.settings.LogErrors {
				fmt.Printf("[ember] Error closing port: %v\n", err)
				os.Stdout.Sync()
			}
		} else if c.settings.LogCommands {
			fmt.Printf("[ember] Port closed\n")
			os.Stdout.Sync()
		}
	case <-time.After(50 * time.Millisecond):
		// Port close timed out - continue anyway since read loop already exited
		if c.settings.LogCommands {
			fmt.Printf("[ember] Port close timed out (continuing - read loop already exited)\n")
			os.Stdout.Sync()
		}
	}

	// The read loop should have already exited when the context was cancelled
	// (we see "Read loop exited" in the logs), so the WaitGroup is already done
	// We can skip waiting since it's already finished
	if c.settings.LogCommands {
		fmt.Printf("[ember] Close() complete (read loop already exited)\n")
		os.Stdout.Sync()
	}

	return nil
}

// Start initializes the controller and returns a channel of incoming messages.
func (c *Controller) Start(ctx context.Context) (<-chan types.IncomingMessage, error) {
	if c.settings.LogCommands {
		fmt.Printf("[ember] Starting controller on port %s...\n", c.settings.Port)
	}

	// Step 0: Perform hardware reset (similar to ZNP)
	// Some EZSP devices need a reset to wake up or enter a known state
	if c.settings.LogCommands {
		fmt.Printf("[ember] Performing hardware reset...\n")
		os.Stdout.Sync()
	}
	if err := c.port.ResetDevice(); err != nil {
		if c.settings.LogErrors {
			fmt.Printf("[ember] WARNING: Hardware reset failed: %v (continuing anyway)\n", err)
			os.Stdout.Sync()
		}
	}

	// Give device time to initialize after reset
	// EZSP devices, especially USB-serial bridges, may need more time
	// Devices with ESP32 bridges (like Sonoff Dongle Max) may send boot messages first
	if c.settings.LogCommands {
		fmt.Printf("[ember] Waiting for device to stabilize after reset (1s)...\n")
		os.Stdout.Sync()
	}
	time.Sleep(1 * time.Second)

	// Check if device is sending any data (bootloader messages, RSTACK, etc.)
	// Some devices send RSTACK automatically after reset
	// Devices with ESP32 bridges may send boot messages - we need to drain those
	// and wait for the EFR32MG24 to become active
	handshakeComplete := false
	if c.settings.LogCommands {
		fmt.Printf("[ember] Checking for device boot messages or RSTACK...\n")
		os.Stdout.Sync()
	}

	// Read for longer to catch ESP32 boot messages and wait for EFR32MG24 to become active
	// Sonoff Dongle Max may send ESP32 boot messages before the EFR32MG24 is ready
	bootData, err := c.port.ReadRawBytes(5*time.Second, 2048) // Increased timeout and buffer size
	if err == nil && len(bootData) > 0 {
		// Check if this looks like ESP32 boot messages (text, not binary ASH frames)
		isText := true
		for _, b := range bootData {
			if b < 0x20 && b != 0x0A && b != 0x0D && b != 0x09 { // Not printable ASCII or common whitespace
				if b != 0x00 { // Allow null bytes
					isText = false
					break
				}
			}
		}

		if c.settings.LogCommands {
			if isText && len(bootData) > 20 {
				// Likely ESP32 boot messages - show first/last part
				preview := string(bootData)
				if len(preview) > 100 {
					preview = preview[:50] + "..." + preview[len(preview)-50:]
				}
				fmt.Printf("[ember] Device sent text data (likely ESP32 boot messages): %q\n", preview)
			} else {
				fmt.Printf("[ember] Device sent data: %X (%q)\n", bootData, bootData)
			}
			os.Stdout.Sync()
		}

		// Check if this is an ASH frame (contains FLAG bytes)
		hasFlag := false
		for _, b := range bootData {
			if b == ASH_FLAG {
				hasFlag = true
				break
			}
		}

		// If we got text data (ESP32 boot messages), wait longer and try to drain
		// The EFR32MG24 might not be active yet - devices with ESP32 bridges may need
		// time for the ESP32 to finish booting and enable the EFR32MG24 bridge
		if isText && !hasFlag {
			if c.settings.LogCommands {
				fmt.Printf("[ember] Detected ESP32 boot messages - waiting for EFR32MG24 to become active...\n")
				fmt.Printf("[ember] Note: Sonoff Dongle Max may need ESP32 to finish booting before EFR32MG24 is accessible\n")
				os.Stdout.Sync()
			}
			// Wait longer - ESP32 bridges may take 5-10 seconds to fully boot
			time.Sleep(5 * time.Second)
			// Try reading again - EFR32MG24 should be active now
			// Read for longer to catch any delayed ASH frames
			moreData, err2 := c.port.ReadRawBytes(5*time.Second, 1024)
			if err2 == nil && len(moreData) > 0 {
				if c.settings.LogCommands {
					// Check if new data is text or binary
					isMoreText := true
					for _, b := range moreData {
						if b < 0x20 && b != 0x0A && b != 0x0D && b != 0x09 {
							if b != 0x00 {
								isMoreText = false
								break
							}
						}
					}
					if isMoreText {
						fmt.Printf("[ember] Still receiving text data (ESP32 may still be booting)\n")
					} else {
						fmt.Printf("[ember] Received binary data (possible ASH frames)\n")
					}
					os.Stdout.Sync()
				}
				// Append to bootData and check again
				bootData = append(bootData, moreData...)
				hasFlag = false
				for _, b := range bootData {
					if b == ASH_FLAG {
						hasFlag = true
						break
					}
				}
				if c.settings.LogCommands && hasFlag {
					fmt.Printf("[ember] Found ASH frames after ESP32 boot completed\n")
					os.Stdout.Sync()
				} else if c.settings.LogCommands && !hasFlag {
					fmt.Printf("[ember] WARNING: No ASH frames detected after waiting. EFR32MG24 may not be active.\n")
					fmt.Printf("[ember] This device may need web console configuration or different firmware.\n")
					os.Stdout.Sync()
				}
			}
		}

		if hasFlag {
			// Try to parse as ASH frame - might be RSTACK
			if c.settings.LogCommands {
				fmt.Printf("[ember] Data contains ASH flag bytes, attempting to parse...\n")
				os.Stdout.Sync()
			}

			// Look for complete ASH frames in the data
			// ASH frames are delimited by FLAG bytes (0x7E)
			// But the device might send: CANCEL + frame data + FLAG
			// So we need to look for frames that might not have a leading FLAG

			// First, try to find frames delimited by FLAG bytes
			for i := 0; i < len(bootData); i++ {
				if bootData[i] == ASH_FLAG {
					// Found start of frame, find end
					for j := i + 1; j < len(bootData); j++ {
						if bootData[j] == ASH_FLAG {
							// Found complete frame
							frameData := bootData[i : j+1]
							if c.settings.LogCommands {
								fmt.Printf("[ember] Found potential ASH frame (FLAG-delimited): %X\n", frameData)
								os.Stdout.Sync()
							}

							ashFrame, parseErr := ParseASHFrame(frameData)
							if parseErr == nil {
								if c.settings.LogCommands {
									fmt.Printf("[ember] Parsed frame type: %v\n", ashFrame.Type)
									os.Stdout.Sync()
								}

								if ashFrame.Type == ASH_FRAME_RSTACK {
									if c.settings.LogCommands {
										fmt.Printf("[ember] Device sent RSTACK automatically! Handshake already complete.\n")
										if len(ashFrame.Data) >= 2 {
											version := ashFrame.Data[0]
											resetCode := ashFrame.Data[1]
											fmt.Printf("[ember] RSTACK version: 0x%02X, reset code: 0x%02X\n", version, resetCode)
											if version != 0x02 {
												fmt.Printf("[ember] WARNING: RSTACK version mismatch! Expected 0x02, got 0x%02X\n", version)
											}
										}
										os.Stdout.Sync()
									}
									// Device already sent RSTACK, we're connected
									// Verify version (should be 0x02 for ASH version 2)
									if len(ashFrame.Data) >= 1 && ashFrame.Data[0] != 0x02 {
										if c.settings.LogErrors {
											fmt.Printf("[ember] ERROR: RSTACK version mismatch! Expected 0x02, got 0x%02X\n", ashFrame.Data[0])
											os.Stdout.Sync()
										}
										// Continue anyway - might still work
									}
									// RSTACK resets the ASH session, so reset sequence numbers
									c.txSeq = 0
									c.rxSeq = 0
									if c.settings.LogCommands {
										fmt.Printf("[ember] Reset sequence numbers after RSTACK: txSeq=0, rxSeq=0\n")
										os.Stdout.Sync()
									}
									handshakeComplete = true
									break
								} else if ashFrame.Type == ASH_FRAME_RST {
									if c.settings.LogCommands {
										fmt.Printf("[ember] Device sent RST automatically\n")
										os.Stdout.Sync()
									}
									// Device sent RST, we should respond with RSTACK or send our own RST
								}
							} else {
								if c.settings.LogErrors {
									fmt.Printf("[ember] Failed to parse frame: %v\n", parseErr)
									os.Stdout.Sync()
								}
							}
							break
						}
					}
					if handshakeComplete {
						break
					}
				}
			}

			// If no FLAG-delimited frame found, try to find RSTACK by looking for 0xC1
			// Some devices send: CANCEL + RSTACK data + FLAG (without leading FLAG)
			if !handshakeComplete {
				for i := 0; i < len(bootData); i++ {
					if bootData[i] == 0xC1 { // RSTACK control byte
						// Found potential RSTACK, try to parse it
						// RSTACK format: C1 [version] [reset_code] [CRC_high] [CRC_low]
						// Minimum is 3 bytes (C1 + CRC), but often has version + reset_code = 5 bytes
						// Look for ending FLAG
						endIdx := -1
						for j := i + 1; j < len(bootData); j++ {
							if bootData[j] == ASH_FLAG {
								endIdx = j
								break
							}
						}

						if endIdx > i {
							// Construct frame with FLAG bytes: FLAG + data + FLAG
							frameData := make([]byte, 0, endIdx-i+2)
							frameData = append(frameData, ASH_FLAG) // Add leading FLAG
							frameData = append(frameData, bootData[i:endIdx]...)
							frameData = append(frameData, ASH_FLAG) // Add trailing FLAG

							if c.settings.LogCommands {
								fmt.Printf("[ember] Found potential RSTACK frame (C1-based): %X\n", frameData)
								os.Stdout.Sync()
							}

							ashFrame, parseErr := ParseASHFrame(frameData)
							if parseErr == nil && ashFrame.Type == ASH_FRAME_RSTACK {
								if c.settings.LogCommands {
									fmt.Printf("[ember] Device sent RSTACK automatically! Handshake already complete.\n")
									if len(ashFrame.Data) >= 2 {
										version := ashFrame.Data[0]
										resetCode := ashFrame.Data[1]
										fmt.Printf("[ember] RSTACK version: 0x%02X, reset code: 0x%02X\n", version, resetCode)
										if version != 0x02 {
											fmt.Printf("[ember] WARNING: RSTACK version mismatch! Expected 0x02, got 0x%02X\n", version)
										}
									}
									os.Stdout.Sync()
								}
								// Verify version (should be 0x02 for ASH version 2)
								if len(ashFrame.Data) >= 1 && ashFrame.Data[0] != 0x02 {
									if c.settings.LogErrors {
										fmt.Printf("[ember] ERROR: RSTACK version mismatch! Expected 0x02, got 0x%02X\n", ashFrame.Data[0])
										os.Stdout.Sync()
									}
									// Continue anyway - might still work
								}
								// RSTACK resets the ASH session, so reset sequence numbers
								c.txSeq = 0
								c.rxSeq = 0
								if c.settings.LogCommands {
									fmt.Printf("[ember] Reset sequence numbers after RSTACK: txSeq=0, rxSeq=0\n")
									os.Stdout.Sync()
								}
								handshakeComplete = true
								break
							}
						}
					}
				}
			}
		} else {
			// Check if it looks like bootloader mode
			if isBootloader, _, _ := c.port.CheckBootloaderMode(); isBootloader {
				if c.settings.LogErrors {
					fmt.Printf("[ember] WARNING: Device appears to be in bootloader mode!\n")
					os.Stdout.Sync()
				}
			}
		}
	} else {
		if c.settings.LogCommands {
			fmt.Printf("[ember] No boot messages detected (device may already be initialized)\n")
			os.Stdout.Sync()
		}
	}

	// Step 1: Perform RST/RSTACK handshake (if not already complete)
	// If device sent RSTACK automatically, the handshake is already done
	// and we should NOT send another RST (device won't respond)
	if !handshakeComplete {
		if c.settings.LogCommands {
			fmt.Printf("[ember] Performing ASH handshake...\n")
			os.Stdout.Sync() // Force flush
		}
		if err := c.performRSTHandshake(); err != nil {
			return nil, fmt.Errorf("ASH handshake: %w", err)
		}
		if c.settings.LogCommands {
			fmt.Printf("[ember] ASH handshake complete\n")
			os.Stdout.Sync()
		}
	} else {
		if c.settings.LogCommands {
			fmt.Printf("[ember] ASH handshake already complete (device sent RSTACK automatically)\n")
			fmt.Printf("[ember] Skipping RST/RSTACK handshake - proceeding directly to version command\n")
			os.Stdout.Sync()
		}
	}

	// Drain any remaining data from the port after handshake
	// This ensures the read loop starts with a clean state
	// Note: We use a timeout-based drain to avoid blocking
	// IMPORTANT: We read RSTACK using ReadRawBytes (bypassing bufio.Reader),
	// so we need to ensure the bufio.Reader buffer is also clear
	if c.settings.LogCommands {
		fmt.Printf("[ember] Draining any remaining data from port (non-blocking)...\n")
		buffered := c.port.Buffered()
		if buffered > 0 {
			fmt.Printf("[ember] WARNING: bufio.Reader has %d buffered bytes (will be drained)\n", buffered)
			os.Stdout.Sync()
		}
	}
	if err := c.port.Drain(); err != nil {
		if c.settings.LogErrors {
			fmt.Printf("[ember] WARNING: Failed to drain port: %v\n", err)
			os.Stdout.Sync()
		}
	}

	// CRITICAL: Synchronize to a frame boundary before starting the read loop
	// This prevents "starting mid-frame" messages by ensuring we start reading
	// at the beginning of a frame (FLAG byte)
	if c.settings.LogCommands {
		fmt.Printf("[ember] Synchronizing to frame boundary (waiting for FLAG byte)...\n")
		os.Stdout.Sync()
	}
	synced, err := c.port.SyncToFrameBoundary(2 * time.Second)
	if err != nil && err != io.ErrNoProgress {
		if c.settings.LogErrors {
			fmt.Printf("[ember] WARNING: Failed to sync to frame boundary: %v (continuing anyway)\n", err)
			os.Stdout.Sync()
		}
	} else if synced {
		if c.settings.LogCommands {
			fmt.Printf("[ember] Synchronized to frame boundary\n")
			os.Stdout.Sync()
		}
	} else {
		if c.settings.LogCommands {
			fmt.Printf("[ember] No FLAG byte found during sync (device may be idle, continuing anyway)\n")
			os.Stdout.Sync()
		}
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Port drain complete\n")
		os.Stdout.Sync()
	}

	// Step 2: Start read loop FIRST so we can receive responses
	// This must be started before sending any commands
	output := make(chan types.IncomingMessage)
	if c.settings.LogCommands {
		fmt.Printf("[ember] Starting read loop goroutine...\n")
		os.Stdout.Sync()
	}
	c.readLoopWg.Add(1)
	go func() {
		defer c.readLoopWg.Done()
		c.readLoop(ctx, output)
	}()

	// Give the read loop a moment to start
	time.Sleep(100 * time.Millisecond)
	if c.settings.LogCommands {
		fmt.Printf("[ember] Read loop should be running now\n")
		os.Stdout.Sync()
	}

	// Wait a bit longer after RSTACK before sending first command
	// Some devices need time to fully initialize after handshake
	if c.settings.LogCommands {
		fmt.Printf("[ember] Waiting additional 500ms after RSTACK before sending version command...\n")
		os.Stdout.Sync()
	}
	time.Sleep(500 * time.Millisecond)

	// Step 3: Send version command to verify communication
	if c.settings.LogCommands {
		fmt.Printf("[ember] Sending version command...\n")
		os.Stdout.Sync()
	}

	// EZSP version command uses legacy format for first command
	// Format: [Sequence=0][Control=0][FrameID=0][Params=desiredProtocolVersion]
	// The documentation example shows 0x02, so we'll try that first to match exactly
	// zigbee-herdsman uses 0x12, but we'll start with 0x02 per documentation
	// Note: The version command should use sequence 0 (legacy format requirement)
	versionFrame := &EZSPFrame{
		Sequence:   0, // Legacy format uses sequence 0
		Control:    EZSP_FRAME_CONTROL_COMMAND,
		FrameID:    EZSP_VERSION,
		Parameters: []byte{0x02}, // EZSP protocol version 2 (per documentation example)
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Version command (legacy format, sequence=0)\n")
		os.Stdout.Sync()
	}

	// Register handler using sequence 0 for legacy format
	handler := c.registerHandler(0, 5*time.Second)

	ezspData := SerializeEZSPFrame(versionFrame)
	// DATA frame control: FrameNum (3 bits) + ReTx (1 bit) + AckNum (3 bits)
	// FrameNum = txSeq, AckNum = rxSeq
	// Note: ReTx bit (bit 3) should be 0 for first transmission
	control := byte((c.txSeq << 4) | (c.rxSeq & 0x07))
	if c.settings.LogCommands {
		fmt.Printf("[ember] Building DATA frame: txSeq=%d, rxSeq=%d, control=0x%02X\n", c.txSeq, c.rxSeq, control)
		os.Stdout.Sync()
	}
	ashDataFrame := buildASHDataFrame(control, ezspData)
	// Increment txSeq after building frame (for next frame)
	c.txSeq = (c.txSeq + 1) % 8

	if c.settings.LogCommands {
		fmt.Printf("[ember] Version command frame: %X\n", ashDataFrame)
		os.Stdout.Sync()
	}
	n, err := c.port.WriteBytes(ashDataFrame)
	if err != nil {
		c.removeHandler(versionFrame.Sequence, handler)
		return nil, fmt.Errorf("sending version command: %w", err)
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Version command sent (%d bytes), waiting for device to process...\n", n)
		os.Stdout.Sync()
	}

	// Give device a moment to process and send ACK/response
	// NOTE: Do NOT use ReadRawBytes here - it reads directly from the connection
	// and would consume data that the read loop needs! Let the read loop handle all incoming data.
	// Give it a bit more time - some devices are slow to respond
	time.Sleep(200 * time.Millisecond)

	if c.settings.LogCommands {
		// Check if read loop has received anything (non-blocking check)
		fmt.Printf("[ember] Checking read loop status (should be waiting for frames)...\n")
		os.Stdout.Sync()
	}

	// Read version response using handler (read loop will route it)
	if c.settings.LogCommands {
		fmt.Printf("[ember] Waiting for version response (timeout: 5s)...\n")
		os.Stdout.Sync()
	}
	versionResponse, err := handler.Receive()
	if err != nil {
		return nil, fmt.Errorf("reading version response: %w", err)
	}

	version, err := ParseVersionResponse(versionResponse.Parameters)
	if err != nil {
		return nil, fmt.Errorf("parsing version response: %w", err)
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Protocol version: %d, Stack type: %d, Stack version: %d\n",
			version.ProtocolVersion, version.StackType, version.StackVersion)
		os.Stdout.Sync()
	}

	return output, nil
}

// performRSTHandshake performs the ASH RST/RSTACK handshake.
func (c *Controller) performRSTHandshake() error {
	if c.settings.LogCommands {
		fmt.Printf("[ember] performRSTHandshake: starting...\n")
		os.Stdout.Sync()
	}

	// First, check if device is sending ANY data at all (raw bytes test)
	if c.settings.LogCommands {
		fmt.Printf("[ember] Testing if device is responsive (reading raw bytes)...\n")
		os.Stdout.Sync()
	}
	rawBytes, err := c.port.ReadRawBytes(200*time.Millisecond, 256)
	if err == nil && len(rawBytes) > 0 {
		if c.settings.LogCommands {
			fmt.Printf("[ember] Device IS sending data: %X (%q)\n", rawBytes, rawBytes)
			os.Stdout.Sync()
		}
	} else {
		if c.settings.LogCommands {
			fmt.Printf("[ember] No data from device yet (this is OK, device may need RST first)\n")
			os.Stdout.Sync()
		}
	}

	// Drain any pending data first (non-blocking)
	if c.settings.LogCommands {
		fmt.Printf("[ember] Draining any pending data from serial port...\n")
		os.Stdout.Sync()
	}
	// Drain is non-blocking (reads with timeout), so this should be safe
	if err := c.port.Drain(); err != nil {
		if c.settings.LogErrors {
			fmt.Printf("[ember] WARNING: Failed to drain port: %v\n", err)
			os.Stdout.Sync()
		}
	}
	if c.settings.LogCommands {
		fmt.Printf("[ember] Drain complete\n")
		os.Stdout.Sync()
	}

	// Some devices (like Sonoff Dongle-E) may send an RST frame automatically
	// Check for device-initiated RST first (non-blocking)
	if c.settings.LogCommands {
		fmt.Printf("[ember] Checking for device-initiated RST frame (timeout: 500ms)...\n")
	}
	rawFrame, err := c.port.ReadASHFrameWithTimeout(500 * time.Millisecond)
	if c.settings.LogCommands {
		if err != nil {
			fmt.Printf("[ember] No device-initiated RST received: %v\n", err)
		} else {
			fmt.Printf("[ember] Received frame from device: %X\n", rawFrame)
		}
	}
	if err == nil {
		ashFrame, parseErr := ParseASHFrame(rawFrame)
		if parseErr == nil && ashFrame.Type == ASH_FRAME_RST {
			if c.settings.LogCommands {
				fmt.Printf("[ember] Device sent RST automatically: %X\n", rawFrame)
			}
		} else if parseErr != nil {
			if c.settings.LogErrors {
				fmt.Printf("[ember] Received data but not valid ASH frame: %v (raw: %X)\n", parseErr, rawFrame)
			}
		} else {
			if c.settings.LogCommands {
				fmt.Printf("[ember] Device sent frame type: %v\n", ashFrame.Type)
			}
		}
	} else if err != io.ErrNoProgress {
		if c.settings.LogCommands {
			fmt.Printf("[ember] No device-initiated RST (this is normal)\n")
		}
	}

	// Send our own RST frame to initiate handshake
	// (Even if device sent RST, we send our own to establish the session)
	rst := buildRSTFrame()
	if c.settings.LogCommands {
		fmt.Printf("[ember] Sending RST frame: %X\n", rst)
	}
	if _, err := c.port.WriteBytes(rst); err != nil {
		return fmt.Errorf("sending RST frame: %w", err)
	}

	// Wait a bit for device to process (especially for USB-serial bridges)
	time.Sleep(200 * time.Millisecond)

	// Try reading raw bytes first to see if device is sending anything
	if c.settings.LogCommands {
		fmt.Printf("[ember] Checking for any incoming data...\n")
	}
	rawBytes, readErr := c.port.ReadRawBytes(500*time.Millisecond, 256)
	if readErr == nil && len(rawBytes) > 0 {
		if c.settings.LogCommands {
			fmt.Printf("[ember] Device sent raw data: %X (%q)\n", rawBytes, rawBytes)
			// Check for ASH flag bytes
			flagCount := 0
			for _, b := range rawBytes {
				if b == ASH_FLAG {
					flagCount++
				}
			}
			if flagCount > 0 {
				fmt.Printf("[ember] Found %d ASH flag bytes (0x7E) in data\n", flagCount)
			}
		}
	}

	// Wait for RSTACK
	if c.settings.LogCommands {
		fmt.Printf("[ember] Waiting for RSTACK response (timeout: 5s)...\n")
	}
	rawFrame, err = c.port.ReadASHFrameWithTimeout(5 * time.Second)
	if err != nil {
		if c.settings.LogErrors {
			fmt.Printf("[ember] Failed to read RSTACK: %v\n", err)
			// Try reading raw bytes one more time to see if device is sending anything
			rawBytes, readErr := c.port.ReadRawBytes(1*time.Second, 256)
			if readErr == nil && len(rawBytes) > 0 {
				fmt.Printf("[ember] Device sent data but not ASH frame: %X (%q)\n", rawBytes, rawBytes)
			} else {
				fmt.Printf("[ember] No data received from device - device appears silent\n")
			}
		}
		return fmt.Errorf("reading RSTACK: %w", err)
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Received frame: %X\n", rawFrame)
	}

	ashFrame, err := ParseASHFrame(rawFrame)
	if err != nil {
		if c.settings.LogErrors {
			fmt.Printf("[ember] Failed to parse frame: %v (raw: %X)\n", err, rawFrame)
		}
		return fmt.Errorf("parsing RSTACK frame: %w", err)
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Parsed frame type: %v\n", ashFrame.Type)
	}

	if ashFrame.Type != ASH_FRAME_RSTACK {
		return fmt.Errorf("expected RSTACK, got %v", ashFrame.Type)
	}

	// RSTACK resets the ASH session, so reset sequence numbers
	c.txSeq = 0
	c.rxSeq = 0
	if c.settings.LogCommands {
		fmt.Printf("[ember] Reset sequence numbers after RSTACK: txSeq=0, rxSeq=0\n")
		os.Stdout.Sync()
	}

	return nil
}

// readEZSPResponse reads an EZSP response frame with the given sequence number.
func (c *Controller) readEZSPResponse(sequence uint8, timeout time.Duration) (*EZSPFrame, error) {
	done := make(chan *EZSPFrame, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			rawFrame, err := c.port.ReadASHFrame()
			if err != nil {
				errCh <- err
				return
			}

			ashFrame, err := ParseASHFrame(rawFrame)
			if err != nil {
				continue
			}

			if ashFrame.Type == ASH_FRAME_DATA {
				ezspFrame, err := ParseEZSPFrame(ashFrame.Data)
				if err != nil {
					continue
				}

				if ezspFrame.Control == EZSP_FRAME_CONTROL_RESPONSE &&
					ezspFrame.Sequence == sequence {
					done <- ezspFrame
					return
				}
			}
		}
	}()

	select {
	case response := <-done:
		return response, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, ErrTimeout
	}
}

// readLoop orchestrates three stages: byte reading, frame parsing, and frame handling.
// This ensures we never stop reading from the serial port.
func (c *Controller) readLoop(ctx context.Context, output chan<- types.IncomingMessage) {
	defer func() {
		close(output)
		if c.settings.LogCommands {
			fmt.Printf("[ember] Read loop exited\n")
			os.Stdout.Sync()
		}
	}()

	if c.settings.LogCommands {
		fmt.Printf("[ember] Read loop started (continuous read architecture)\n")
		os.Stdout.Sync()
	}

	// Stage 1: Byte reader - continuously reads bytes from serial port
	byteCh := make(chan byte, 256) // Buffered to prevent blocking the reader
	byteReaderDone := make(chan struct{})

	// Stage 2: Frame parser - extracts ASH frames from byte stream
	frameCh := make(chan []byte, 32) // Buffered for complete frames
	parserDone := make(chan struct{})

	// Start byte reader (Stage 1)
	go c.byteReader(ctx, byteCh, byteReaderDone)

	// Start frame parser (Stage 2)
	go c.frameParser(ctx, byteCh, frameCh, parserDone)

	// Stage 3: Frame handler - processes complete ASH frames (main loop)
	for {
		select {
		case <-ctx.Done():
			if c.settings.LogCommands {
				fmt.Printf("[ember] Read loop: context cancelled\n")
				os.Stdout.Sync()
			}
			// Wait for parser and reader to finish
			<-parserDone
			<-byteReaderDone
			return
		case <-c.done:
			if c.settings.LogCommands {
				fmt.Printf("[ember] Read loop: controller done\n")
				os.Stdout.Sync()
			}
			// Wait for parser and reader to finish
			<-parserDone
			<-byteReaderDone
			return
		case rawFrame, ok := <-frameCh:
			if !ok {
				// Parser closed channel (error or done)
				if c.settings.LogCommands {
					fmt.Printf("[ember] Frame parser closed channel\n")
					os.Stdout.Sync()
				}
				<-byteReaderDone
				return
			}

			// Process the frame
			c.handleASHFrame(rawFrame, output)
		}
	}
}

// byteReader continuously reads chunks from the serial port and writes bytes to byteCh.
// This matches the ZNP implementation: read chunks for better performance, then send bytes individually.
// This is more efficient than byte-by-byte reading and reduces buffer overruns.
func (c *Controller) byteReader(ctx context.Context, byteCh chan<- byte, done chan<- struct{}) {
	defer close(done)
	defer close(byteCh)

	if c.settings.LogCommands {
		fmt.Printf("[ember] Byte reader started (chunk-based continuous read)\n")
		os.Stdout.Sync()
	}

	// Read in chunks for better performance (like ZNP and zigbee-herdsman)
	chunkBuf := make([]byte, 256)

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		default:
		}

		// Read a chunk from the serial port (more efficient than byte-by-byte)
		// Access the port's bufio.Reader directly for chunk reading
		n, err := c.port.ReadBytes(chunkBuf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) || err == io.EOF {
				if c.settings.LogCommands {
					fmt.Printf("[ember] Byte reader: port closed\n")
					os.Stdout.Sync()
				}
				return
			}
			if c.settings.LogErrors {
				fmt.Printf("[ember] Byte reader error: %v\n", err)
				os.Stdout.Sync()
			}
			// Continue reading on non-fatal errors
			continue
		}

		if n == 0 {
			continue
		}

		// Send bytes from the chunk individually to maintain byte-by-byte parsing logic
		for i := 0; i < n; i++ {
			b := chunkBuf[i]
			// Send byte to parser (non-blocking if channel is full)
			select {
			case byteCh <- b:
			case <-ctx.Done():
				return
			case <-c.done:
				return
			}
		}
	}
}

// frameParser reads bytes from byteCh, maintains a buffer, and extracts complete ASH frames.
// ASH frames are delimited by FLAG bytes (0x7E). The parser collects all bytes between FLAGs
// (including the FLAGs themselves) and sends complete frames to frameCh.
// Byte stuffing (0x7D escape sequences) is handled by ParseASHFrame, not here.
func (c *Controller) frameParser(ctx context.Context, byteCh <-chan byte, frameCh chan<- []byte, done chan<- struct{}) {
	defer close(done)
	defer close(frameCh)

	if c.settings.LogCommands {
		fmt.Printf("[ember] Frame parser started\n")
		os.Stdout.Sync()
	}

	var currentFrame []byte
	var garbageBuffer []byte // Bytes before first FLAG (might be mid-frame start)

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case b, ok := <-byteCh:
			if !ok {
				// Byte reader closed channel
				return
			}

			if b == ASH_FLAG {
				if len(currentFrame) > 0 {
					// End of frame - we have a complete frame (FLAG...FLAG)
					currentFrame = append(currentFrame, b)
					// Only send if frame has content (not just FLAG...FLAG)
					if len(currentFrame) > 2 {
						// Send complete frame to handler
						select {
						case frameCh <- currentFrame:
						case <-ctx.Done():
							return
						case <-c.done:
							return
						}
					}
					currentFrame = nil
				} else {
					// Start of new frame
					// Check if we have garbage bytes before this FLAG (might be mid-frame start)
					if len(garbageBuffer) > 0 {
						// Try to reconstruct frame from garbage + current FLAG
						potentialFrame := make([]byte, 0, len(garbageBuffer)+2)
						potentialFrame = append(potentialFrame, ASH_FLAG) // Add leading FLAG
						potentialFrame = append(potentialFrame, garbageBuffer...)
						potentialFrame = append(potentialFrame, ASH_FLAG) // Add trailing FLAG

						// Try to parse it
						_, parseErr := ParseASHFrame(potentialFrame)
						if parseErr == nil {
							// Successfully reconstructed - send it
							select {
							case frameCh <- potentialFrame:
							case <-ctx.Done():
								return
							case <-c.done:
								return
							}
						} else if len(garbageBuffer) > 3 {
							// Couldn't reconstruct - log if significant garbage
							if c.settings.LogErrors {
								fmt.Printf("[ember] Frame parser: discarded %d bytes before FLAG (could not reconstruct)\n", len(garbageBuffer))
								os.Stdout.Sync()
							}
						}
						garbageBuffer = nil
					}
					// Start new frame with this FLAG
					currentFrame = []byte{b}
				}
			} else {
				// Regular byte
				if len(currentFrame) > 0 {
					// We're in a frame - add byte to current frame
					currentFrame = append(currentFrame, b)
				} else {
					// Not in a frame yet - buffer it (might be garbage or mid-frame start)
					garbageBuffer = append(garbageBuffer, b)
					// Limit buffer size to prevent memory issues
					if len(garbageBuffer) > 256 {
						if c.settings.LogErrors {
							fmt.Printf("[ember] Frame parser: garbage buffer too large (%d bytes), clearing\n", len(garbageBuffer))
							os.Stdout.Sync()
						}
						garbageBuffer = nil
					}
				}
			}
		}
	}
}

// handleASHFrame processes a complete ASH frame (parsing, ACK, EZSP handling).
func (c *Controller) handleASHFrame(rawFrame []byte, output chan<- types.IncomingMessage) {
	if c.settings.LogCommands {
		fmt.Printf("[ember] Handling ASH frame, raw=%X\n", rawFrame)
		os.Stdout.Sync()
	}

	ashFrame, err := ParseASHFrame(rawFrame)
	if err != nil {
		if c.settings.LogErrors {
			fmt.Printf("[ember] Error parsing ASH frame: %v (raw: %X)\n", err, rawFrame)
			os.Stdout.Sync()
		}
		return
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Parsed ASH frame type=%v\n", ashFrame.Type)
		os.Stdout.Sync()
	}

	switch ashFrame.Type {
	case ASH_FRAME_DATA:
		if c.settings.LogCommands {
			fmt.Printf("[ember] Received DATA frame: FrameNum=%d, AckNum=%d, raw=%X\n", ashFrame.FrameNum, ashFrame.AckNum, rawFrame)
			os.Stdout.Sync()
		}

		// When we receive a DATA frame, we MUST send an ACK frame
		// Update our expected receive sequence if frame is in sequence
		if ashFrame.FrameNum == c.rxSeq {
			c.rxSeq = (c.rxSeq + 1) % 8
		}

		// Send ACK frame immediately (ackNum is the next frame we expect)
		ackFrame := buildASHACKFrame(c.rxSeq)
		if c.settings.LogCommands {
			fmt.Printf("[ember] Sending ACK: ackNum=%d (frame: %X)\n", c.rxSeq, ackFrame)
			os.Stdout.Sync()
		}
		if _, err := c.port.WriteBytes(ackFrame); err != nil {
			if c.settings.LogErrors {
				fmt.Printf("[ember] Failed to send ACK: %v\n", err)
				os.Stdout.Sync()
			}
		}

		// Derandomize the Data Field (XOR with pseudo-random sequence to restore original)
		derandomizedData := derandomizeData(ashFrame.Data)

		// Now process the EZSP frame
		ezspFrame, err := ParseEZSPFrame(derandomizedData)
		if err != nil {
			if c.settings.LogErrors {
				fmt.Printf("[ember] Error parsing EZSP frame: %v (data: %X, derandomized: %X)\n", err, ashFrame.Data, derandomizedData)
				os.Stdout.Sync()
			}
			return // Changed from continue to return since we're in a function now
		}

		if c.settings.LogCommands {
			fmt.Printf("[ember] Parsed EZSP frame: Sequence=%d, Control=%d, FrameID=0x%02X, ParamsLen=%d\n",
				ezspFrame.Sequence, ezspFrame.Control, ezspFrame.FrameID, len(ezspFrame.Parameters))
			os.Stdout.Sync()
		}

		// Handle response frames
		// EZSP response frames have bit 7 set (0x80) in the control byte
		if ezspFrame.Control&0x80 != 0 {
			if c.settings.LogCommands {
				fmt.Printf("[ember] EZSP response frame: Sequence=%d, looking for handler...\n", ezspFrame.Sequence)
				os.Stdout.Sync()
			}

			c.handlerMux.Lock()
			handler := c.handlers[ezspFrame.Sequence]
			if handler != nil {
				delete(c.handlers, ezspFrame.Sequence)
				if c.settings.LogCommands {
					fmt.Printf("[ember] Found handler for sequence %d\n", ezspFrame.Sequence)
					os.Stdout.Sync()
				}
			} else {
				if c.settings.LogCommands {
					fmt.Printf("[ember] No handler found for sequence %d (available handlers: %v)\n",
						ezspFrame.Sequence, c.getHandlerSequences())
					os.Stdout.Sync()
				}
			}
			c.handlerMux.Unlock()

			if handler != nil {
				handler.fulfill(ezspFrame)
			} else {
				// Handle callbacks (incoming messages, etc.)
				c.handleCallback(ezspFrame, output)
			}
		} else {
			// Command frame from NCP (shouldn't happen, but handle gracefully)
			if c.settings.LogCommands {
				fmt.Printf("[ember] EZSP command frame from NCP (unexpected), FrameID=0x%02X\n", ezspFrame.FrameID)
				os.Stdout.Sync()
			}
		}

	case ASH_FRAME_ACK:
		// Device acknowledged our DATA frame
		if c.settings.LogCommands {
			fmt.Printf("[ember] Received ACK, ackNum=%d (expected next frame: %d)\n", ashFrame.AckNum, (c.txSeq-1)%8)
			os.Stdout.Sync()
		}
		// ACK indicates device received our frame successfully
		// The ackNum tells us which frame the device expects next
		// We don't need to update txSeq here - it's already incremented when we sent the frame

	case ASH_FRAME_NAK:
		// Device rejected our DATA frame
		if c.settings.LogErrors {
			fmt.Printf("[ember] Received NAK, ackNum=%d\n", ashFrame.AckNum)
		}
		// TODO: Handle retransmission

	case ASH_FRAME_RSTACK:
		// Unexpected RSTACK (we already handled it during init)
		if c.settings.LogErrors {
			fmt.Printf("[ember] Received unexpected RSTACK\n")
		}

	case ASH_FRAME_ERROR:
		// Device error
		if c.settings.LogErrors {
			fmt.Printf("[ember] Received ERROR frame\n")
		}
	}
}

// handleCallback processes EZSP callback frames (like incoming messages).
func (c *Controller) handleCallback(frame *EZSPFrame, output chan<- types.IncomingMessage) {
	switch frame.FrameID {
	case EZSP_INCOMING_MESSAGE_HANDLER:
		// TODO: Parse incoming message and send to output channel
		if c.settings.LogCommands {
			fmt.Printf("[ember] Incoming message callback (not yet fully implemented)\n")
		}
	case EZSP_STACK_STATUS_HANDLER:
		// STACK_STATUS_HANDLER callback: [status:uint8]
		// Status values: 0x90 = NETWORK_UP (SLStatus), 0x91 = NETWORK_DOWN, 0x15 = NETWORK_UP (EmberStatus)
		if len(frame.Parameters) >= 1 {
			status := frame.Parameters[0]
			if c.settings.LogCommands {
				fmt.Printf("[ember] STACK_STATUS callback: 0x%02X\n", status)
				os.Stdout.Sync()
			}
			// Send status to channel (non-blocking)
			select {
			case c.stackStatusCh <- status:
			default:
				// Channel full, skip (shouldn't happen with buffered channel)
			}
		}
	}
}

func (c *Controller) registerHandler(sequence uint8, timeout time.Duration) *Handler {
	handler := newHandler()

	c.handlerMux.Lock()
	c.handlers[sequence] = handler
	c.handlerMux.Unlock()

	if timeout > 0 {
		handler.timer = time.AfterFunc(timeout, func() {
			c.removeHandler(sequence, handler)
		})
	}

	return handler
}

func (c *Controller) removeHandler(sequence uint8, handler *Handler) {
	c.handlerMux.Lock()
	if c.handlers[sequence] == handler {
		delete(c.handlers, sequence)
	}
	c.handlerMux.Unlock()

	handler.fail()
}

// getHandlerSequences returns a list of active handler sequence numbers (for debugging).
func (c *Controller) getHandlerSequences() []uint8 {
	c.handlerMux.Lock()
	defer c.handlerMux.Unlock()
	sequences := make([]uint8, 0, len(c.handlers))
	for seq := range c.handlers {
		sequences = append(sequences, seq)
	}
	return sequences
}

// Send sends a message to a device on the Zigbee network.
func (c *Controller) Send(ctx context.Context, msg types.OutgoingMessage) error {
	// TODO: Implement EZSP_SEND_UNICAST command
	return fmt.Errorf("Ember Send not yet implemented")
}

// PermitJoining enables or disables device joining on the network.
// If enabled is true, joining is permitted for 254 seconds (0xFE).
// If enabled is false, joining is disabled (duration 0).
// Note: The EZSP_PERMIT_JOINING command takes a duration in seconds (0-254).
// Duration 0xFF means "use the default timeout" (typically 180 seconds).
// Duration 0xFE means "permit joining indefinitely until explicitly disabled".
func (c *Controller) PermitJoining(ctx context.Context, enabled bool) error {
	var duration uint8
	if enabled {
		// Use 0xFE to permit joining indefinitely (until explicitly disabled)
		// Alternatively, we could use a specific duration in seconds
		duration = 0xFE
	} else {
		duration = 0x00
	}

	// Send EZSP_PERMIT_JOINING command
	// Format: [duration:uint8]
	resp, err := c.sendEZSPCommand(EZSP_PERMIT_JOINING, []byte{duration}, 5*time.Second)
	if err != nil {
		return fmt.Errorf("sending permit joining command: %w", err)
	}

	// Response format: [status:uint8]
	if len(resp.Parameters) < 1 {
		return fmt.Errorf("permit joining response too short")
	}

	status := resp.Parameters[0]
	if status != 0x00 { // SUCCESS
		return fmt.Errorf("permit joining command failed with status 0x%02X", status)
	}

	if c.settings.LogCommands {
		if enabled {
			fmt.Printf("[ember] Permit joining enabled (duration: %d seconds)\n", duration)
		} else {
			fmt.Printf("[ember] Permit joining disabled\n")
		}
		os.Stdout.Sync()
	}

	return nil
}

// sendEZSPCommand sends an EZSP command and waits for a response.
// Returns the EZSP frame response.
func (c *Controller) sendEZSPCommand(frameID EZSPFrameID, params []byte, timeout time.Duration) (*EZSPFrame, error) {
	// Get next sequence number
	seqNum := c.port.NextSequence()

	// Create EZSP frame (using extended format after version is set)
	ezspFrame := &EZSPFrame{
		Sequence:   seqNum,
		Control:    EZSP_FRAME_CONTROL_COMMAND,
		FrameID:    frameID,
		Parameters: params,
	}

	// Register handler BEFORE sending
	handler := c.registerHandler(seqNum, timeout)

	// Serialize and send
	ezspData := SerializeEZSPFrame(ezspFrame)
	control := byte((c.txSeq << 4) | (c.rxSeq & 0x07))
	ashDataFrame := buildASHDataFrame(control, ezspData)
	c.txSeq = (c.txSeq + 1) % 8

	if _, err := c.port.WriteBytes(ashDataFrame); err != nil {
		c.removeHandler(seqNum, handler)
		return nil, fmt.Errorf("sending EZSP command 0x%02X: %w", frameID, err)
	}

	// Wait for response
	response, err := handler.Receive()
	if err != nil {
		return nil, fmt.Errorf("waiting for EZSP command 0x%02X response: %w", frameID, err)
	}

	return response, nil
}

// GetNetworkInfo returns information about the current network state.
func (c *Controller) GetNetworkInfo(ctx context.Context) (*types.NetworkInfo, error) {
	// Get network state first
	networkStateResp, err := c.sendEZSPCommand(EZSP_NETWORK_STATE, nil, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("getting network state: %w", err)
	}

	if len(networkStateResp.Parameters) < 1 {
		return nil, fmt.Errorf("network state response too short")
	}
	networkStatus := networkStateResp.Parameters[0]

	// Map network status to NetworkState enum
	var networkState types.NetworkState
	switch networkStatus {
	case 0x90: // EMBER_NETWORK_UP or SLStatus.NETWORK_UP
		networkState = types.NetworkStateUp
	case 0x91: // EMBER_NETWORK_DOWN or SLStatus.NETWORK_DOWN
		networkState = types.NetworkStateDown
	case 0x93: // EMBER_NOT_JOINED or SLStatus.NOT_JOINED
		networkState = types.NetworkStateNotJoined
	case 0x58: // SLStatus.TRANSMIT_BLOCKED - treat as not joined
		networkState = types.NetworkStateNotJoined
	default:
		// Unknown status - treat as not joined
		networkState = types.NetworkStateNotJoined
	}

	// If not joined or network is down, return minimal info
	if networkState == types.NetworkStateNotJoined || networkState == types.NetworkStateDown {
		return &types.NetworkInfo{
			State: networkState,
		}, nil
	}

	// Only continue if network is up
	if networkState != types.NetworkStateUp {
		return &types.NetworkInfo{
			State: networkState,
		}, nil
	}

	// Get network parameters
	networkParamsResp, err := c.sendEZSPCommand(EZSP_GET_NETWORK_PARAMETERS, nil, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("getting network parameters: %w", err)
	}

	// Parse network parameters response
	// Format: [status:uint8][nodeType:uint8][extendedPanId:8 bytes][panId:uint16][radioTxPower:int8][radioChannel:uint8][joinMethod:uint8][nwkManagerId:uint16][nwkUpdateId:uint8][channels:uint32]
	if len(networkParamsResp.Parameters) < 1 {
		return nil, fmt.Errorf("network parameters response too short: %d bytes", len(networkParamsResp.Parameters))
	}

	offset := 0
	status := networkParamsResp.Parameters[offset]
	offset++

	// Check status first - if error, return early
	// Status 0x00 = SUCCESS, 0x93 = NOT_JOINED, others are errors
	if status != 0x00 {
		if status == 0x93 { // NOT_JOINED
			// Device is not joined to a network, return minimal info
			return &types.NetworkInfo{
				State: types.NetworkStateNotJoined,
			}, nil
		}
		// Other error status - return error with unknown state
		return &types.NetworkInfo{
			State: types.NetworkStateUnknown,
		}, nil
	}

	// Status is SUCCESS, now check we have enough data
	if len(networkParamsResp.Parameters) < 20 {
		return nil, fmt.Errorf("network parameters response too short for success response: %d bytes", len(networkParamsResp.Parameters))
	}

	// Skip nodeType (not needed for NetworkInfo)
	_ = networkParamsResp.Parameters[offset]
	offset++

	// Extended PAN ID (8 bytes)
	extendedPanIDBytes := networkParamsResp.Parameters[offset : offset+8]
	offset += 8
	extendedPanID := binary.LittleEndian.Uint64(extendedPanIDBytes)

	// PAN ID (uint16, little endian)
	panID := binary.LittleEndian.Uint16(networkParamsResp.Parameters[offset : offset+2])
	offset += 2

	// Radio TX Power (int8, skip)
	offset++

	// Radio Channel (uint8)
	channel := uint16(networkParamsResp.Parameters[offset])
	offset++

	// Skip remaining fields (joinMethod, nwkManagerId, nwkUpdateId, channels)

	// Get node ID (short address)
	nodeIDResp, err := c.sendEZSPCommand(EZSP_GET_NODE_ID, nil, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("getting node ID: %w", err)
	}

	if len(nodeIDResp.Parameters) < 2 {
		return nil, fmt.Errorf("node ID response too short")
	}
	shortAddress := binary.LittleEndian.Uint16(nodeIDResp.Parameters[0:2])

	// Get parent/child parameters
	parentChildResp, err := c.sendEZSPCommand(EZSP_GET_PARENT_CHILD_PARAMETERS, nil, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("getting parent/child parameters: %w", err)
	}

	// Format: [childCount:uint8][parentEui64:8 bytes][parentNodeId:uint16]
	if len(parentChildResp.Parameters) < 11 {
		return nil, fmt.Errorf("parent/child parameters response too short")
	}

	parentOffset := 1 // Skip childCount
	parentEui64Bytes := parentChildResp.Parameters[parentOffset : parentOffset+8]
	parentOffset += 8
	parentEui64 := binary.LittleEndian.Uint64(parentEui64Bytes)
	parentNodeID := binary.LittleEndian.Uint16(parentChildResp.Parameters[parentOffset : parentOffset+2])

	return &types.NetworkInfo{
		ShortAddress:          shortAddress,
		PanID:                 panID,
		ParentAddress:         parentNodeID,
		ExtendedPanID:         extendedPanID,
		ExtendedParentAddress: parentEui64,
		Channel:               channel,
		State:                 networkState,
	}, nil
}

// FormNetwork creates a new Zigbee network with the specified parameters.
// This must be called before Start() if the device is not already part of a network.
// The network parameters should be persisted so the same network can be restored
// when swapping devices.
func (c *Controller) FormNetwork(ctx context.Context, params types.NetworkParameters) error {
	if c.settings.LogCommands {
		fmt.Printf("[ember] Forming network: PAN ID=0x%04X, Extended PAN ID=0x%016X, Channel=%d\n",
			params.PanID, params.ExtendedPanID, params.Channel)
		os.Stdout.Sync()
	}

	// Step -1: Force LEAVE_NETWORK first to ensure device is in a clean state
	// This is a workaround for the persistent 0x58 issue - the device may be stuck
	// in an invalid state. Forcing a leave may help reset it.
	if c.settings.LogCommands {
		fmt.Printf("[ember] Step -1: Forcing LEAVE_NETWORK to ensure clean state...\n")
		os.Stdout.Sync()
	}

	// Try to leave network regardless of current state
	// This may fail or return 0x58, but we continue anyway
	leaveResp, leaveErr := c.sendEZSPCommand(EZSP_LEAVE_NETWORK, nil, 5*time.Second)
	if leaveErr == nil && len(leaveResp.Parameters) >= 1 {
		leaveStatus := leaveResp.Parameters[0]
		if c.settings.LogCommands {
			fmt.Printf("[ember] LEAVE_NETWORK returned status: 0x%02X\n", leaveStatus)
			os.Stdout.Sync()
		}
	} else if c.settings.LogCommands {
		fmt.Printf("[ember] LEAVE_NETWORK failed or timed out (continuing anyway): %v\n", leaveErr)
		os.Stdout.Sync()
	}

	// Wait for device to process the leave command
	time.Sleep(1 * time.Second)

	// CRITICAL: Always call NETWORK_INIT first to restore network from tokens/NV memory
	// According to EZSP documentation and zigbee-herdsman:
	// - NETWORK_INIT should be called on startup whether or not the node was previously part of a network
	// - If it returns SUCCESS (0x00), the device restored the network from stored state
	// - If it returns EMBER_NOT_JOINED (0x93), no network exists and we need to form one
	// - We should NOT call SET_INITIAL_SECURITY_STATE or FORM_NETWORK if NETWORK_INIT successfully restored a network
	//
	// This is the key insight: The device may already have network state stored in tokens/NV memory
	// from a previous session. If we try to form a network when one already exists, the device may
	// reject commands with 0x58 (TRANSMIT_BLOCKED) or other errors.
	if c.settings.LogCommands {
		fmt.Printf("[ember] Step 0: Calling NETWORK_INIT to restore network from tokens (if any)...\n")
		os.Stdout.Sync()
	}

	// EmberNetworkInitStruct: bitmask (uint16, little-endian)
	// zigbee-herdsman uses: PARENT_INFO_IN_TOKEN (0x01) | END_DEVICE_REJOIN_ON_REBOOT (0x02) = 0x03
	networkInitParams := []byte{0x03, 0x00} // Little-endian: 0x0003

	networkInitResp, err := c.sendEZSPCommand(EZSP_NETWORK_INIT, networkInitParams, 5*time.Second)
	if err != nil {
		if c.settings.LogErrors {
			fmt.Printf("[ember] NETWORK_INIT failed: %v (will attempt to form new network)\n", err)
			os.Stdout.Sync()
		}
		// Continue to form network
	} else if len(networkInitResp.Parameters) >= 1 {
		networkInitStatus := networkInitResp.Parameters[0]
		if c.settings.LogCommands {
			fmt.Printf("[ember] NETWORK_INIT returned status: 0x%02X\n", networkInitStatus)
			os.Stdout.Sync()
		}

		// Status 0x00 = SUCCESS: Network was restored from tokens
		// Status 0x93 = EMBER_NOT_JOINED: No network exists, need to form
		// Status 0x58 = TRANSMIT_BLOCKED: Unknown state (device may be in invalid state)
		if networkInitStatus == 0x00 {
			// Network was restored! Wait for STACK_STATUS_NETWORK_UP callback
			// Then verify network parameters match what we want
			if c.settings.LogCommands {
				fmt.Printf("[ember] Network restored from tokens - waiting for NETWORK_UP callback...\n")
				os.Stdout.Sync()
			}

			// Wait up to 5 seconds for network to come up
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				// Poll NETWORK_STATE to check if network is up
				stateResp, err := c.sendEZSPCommand(EZSP_NETWORK_STATE, nil, 2*time.Second)
				if err == nil && len(stateResp.Parameters) >= 1 {
					state := stateResp.Parameters[0]
					if state == 2 { // JOINED_NETWORK
						// Network is up! Verify parameters match
						if c.settings.LogCommands {
							fmt.Printf("[ember] Network is UP! Verifying parameters match desired network...\n")
							os.Stdout.Sync()
						}

						// Get network parameters and compare
						currentInfo, err := c.GetNetworkInfo(ctx)
						if err == nil && currentInfo.State == types.NetworkStateUp {
							panIDMatch := currentInfo.PanID == params.PanID
							extendedPanIDMatch := currentInfo.ExtendedPanID == params.ExtendedPanID
							channelMatch := uint8(currentInfo.Channel) == params.Channel

							if panIDMatch && extendedPanIDMatch && channelMatch {
								if c.settings.LogCommands {
									fmt.Printf("[ember] Restored network matches desired parameters - no need to form\n")
									os.Stdout.Sync()
								}
								return nil // Network already exists and matches!
							} else {
								// Network exists but parameters don't match - need to leave and form new
								if c.settings.LogCommands {
									fmt.Printf("[ember] Restored network parameters don't match - leaving network...\n")
									fmt.Printf("[ember] Current: PAN ID=0x%04X, Extended PAN ID=0x%016X, Channel=%d\n",
										currentInfo.PanID, currentInfo.ExtendedPanID, currentInfo.Channel)
									fmt.Printf("[ember] Desired: PAN ID=0x%04X, Extended PAN ID=0x%016X, Channel=%d\n",
										params.PanID, params.ExtendedPanID, params.Channel)
									os.Stdout.Sync()
								}
								// Leave network and continue to form new one
								leaveResp, _ := c.sendEZSPCommand(EZSP_LEAVE_NETWORK, nil, 5*time.Second)
								if len(leaveResp.Parameters) >= 1 {
									if c.settings.LogCommands {
										fmt.Printf("[ember] LEAVE_NETWORK returned: 0x%02X\n", leaveResp.Parameters[0])
										os.Stdout.Sync()
									}
								}
								time.Sleep(1 * time.Second) // Wait for network to go down
								// Continue to form network below
							}
						} else {
							// Couldn't get network info, but network is up - assume it's correct
							if c.settings.LogCommands {
								fmt.Printf("[ember] Network is up but couldn't verify parameters - assuming correct\n")
								os.Stdout.Sync()
							}
							return nil
						}
						break
					}
				}
				time.Sleep(500 * time.Millisecond)
			}

			// If we get here, network didn't come up in time or parameters didn't match
			// Continue to form network below
		} else if networkInitStatus == 0x93 {
			// EMBER_NOT_JOINED - no network exists, proceed to form
			if c.settings.LogCommands {
				fmt.Printf("[ember] No network exists (EMBER_NOT_JOINED) - proceeding to form network\n")
				os.Stdout.Sync()
			}
			// Continue to form network below
		} else {
			// Unknown status (likely 0x58) - log but continue
			if c.settings.LogErrors {
				fmt.Printf("[ember] NETWORK_INIT returned unexpected status 0x%02X - will attempt to form network anyway\n", networkInitStatus)
				os.Stdout.Sync()
			}
			// Continue to form network below
		}
	}

	// Step 1: Set initial security state with Network Key
	// Only reached if NETWORK_INIT indicated no network exists or network parameters don't match
	// This must be done before forming the network
	// Bitmask: TRUST_CENTER_GLOBAL_LINK_KEY | HAVE_PRECONFIGURED_KEY | HAVE_NETWORK_KEY
	// Use well-known link key (Zigbee Profile Interoperability Link Key) for preconfigured key
	// This is: 0x5A 0x69 0x67 0x42 0x65 0x65 0x41 0x6C 0x6C 0x69 0x61 0x6E 0x63 0x65 0x30 0x39
	wellKnownLinkKey := []byte{
		0x5A, 0x69, 0x67, 0x42, 0x65, 0x65, 0x41, 0x6C,
		0x6C, 0x69, 0x61, 0x6E, 0x63, 0x65, 0x30, 0x39,
	}

	// Build initial security state
	// Format: [bitmask:uint16][preconfiguredKey:16 bytes][networkKey:16 bytes][networkKeySequenceNumber:uint8][preconfiguredTrustCenterEui64:8 bytes]
	securityParams := make([]byte, 0, 2+16+16+1+8)

	// Bitmask: TRUST_CENTER_GLOBAL_LINK_KEY (0x0004) | HAVE_PRECONFIGURED_KEY (0x0100) | HAVE_NETWORK_KEY (0x0200)
	bitmask := uint16(0x0004 | 0x0100 | 0x0200)
	securityParams = append(securityParams, byte(bitmask), byte(bitmask>>8))

	// Preconfigured key (well-known link key)
	securityParams = append(securityParams, wellKnownLinkKey...)

	// Network key
	securityParams = append(securityParams, params.NetworkKey[:]...)

	// Network key sequence number (0 for new network)
	securityParams = append(securityParams, 0x00)

	// Preconfigured Trust Center EUI64 (all zeros - will be learned during formation)
	securityParams = append(securityParams, make([]byte, 8)...)

	if c.settings.LogCommands {
		fmt.Printf("[ember] Setting initial security state...\n")
		os.Stdout.Sync()
	}

	securityResp, err := c.sendEZSPCommand(EZSP_SET_INITIAL_SECURITY_STATE, securityParams, 5*time.Second)
	if err != nil {
		return fmt.Errorf("setting initial security state: %w", err)
	}

	if len(securityResp.Parameters) < 1 {
		return fmt.Errorf("security state response too short")
	}

	securityStatus := securityResp.Parameters[0]
	// Status 0x00 = SUCCESS
	// Status 0x58 = TRANSMIT_BLOCKED - device may already have security configured or be in invalid state
	// We treat 0x58 as a warning but continue, similar to how we handle config commands
	if securityStatus == 0x00 {
		if c.settings.LogCommands {
			fmt.Printf("[ember] Initial security state set successfully\n")
			os.Stdout.Sync()
		}
	} else if securityStatus == 0x58 {
		// Device returned 0x58 - may already be configured or in invalid state
		// Log warning but continue - the device may still be able to form a network
		if c.settings.LogErrors {
			fmt.Printf("[ember] WARNING: SET_INITIAL_SECURITY_STATE returned 0x58 (device may already be configured)\n")
			fmt.Printf("[ember] Continuing with network formation anyway...\n")
			os.Stdout.Sync()
		}
	} else {
		// Other error status - this is a real problem
		return fmt.Errorf("setting initial security state failed with status 0x%02X", securityStatus)
	}

	// Step 1.5: Set extended security bitmask (like zigbee-herdsman does)
	// Extended security bitmask: JOINER_GLOBAL_LINK_KEY | NWK_LEAVE_REQUEST_NOT_ALLOWED
	// JOINER_GLOBAL_LINK_KEY = 0x0001, NWK_LEAVE_REQUEST_NOT_ALLOWED = 0x0002
	if c.settings.LogCommands {
		fmt.Printf("[ember] Setting extended security bitmask...\n")
		os.Stdout.Sync()
	}

	extendedBitmask := uint16(0x0001 | 0x0002) // JOINER_GLOBAL_LINK_KEY | NWK_LEAVE_REQUEST_NOT_ALLOWED
	extendedSecurityResp, err := c.sendEZSPCommand(EZSP_SET_EXTENDED_SECURITY_BITMASK,
		[]byte{byte(extendedBitmask), byte(extendedBitmask >> 8)}, 5*time.Second)
	if err != nil {
		if c.settings.LogErrors {
			fmt.Printf("[ember] WARNING: SET_EXTENDED_SECURITY_BITMASK failed: %v (continuing anyway)\n", err)
			os.Stdout.Sync()
		}
	} else if len(extendedSecurityResp.Parameters) >= 1 {
		extendedStatus := extendedSecurityResp.Parameters[0]
		if extendedStatus == 0x00 {
			if c.settings.LogCommands {
				fmt.Printf("[ember] Extended security bitmask set successfully\n")
				os.Stdout.Sync()
			}
		} else if extendedStatus == 0x58 {
			if c.settings.LogErrors {
				fmt.Printf("[ember] WARNING: SET_EXTENDED_SECURITY_BITMASK returned 0x58 (continuing anyway)\n")
				os.Stdout.Sync()
			}
		} else {
			if c.settings.LogErrors {
				fmt.Printf("[ember] WARNING: SET_EXTENDED_SECURITY_BITMASK returned 0x%02X (continuing anyway)\n", extendedStatus)
				os.Stdout.Sync()
			}
		}
	}

	// Step 1.6: Clear key table before forming network (like zigbee-herdsman does)
	// This ensures we start with a clean key table
	if c.settings.LogCommands {
		fmt.Printf("[ember] Clearing key table before forming network...\n")
		os.Stdout.Sync()
	}
	clearKeyResp, clearKeyErr := c.sendEZSPCommand(EZSP_CLEAR_KEY_TABLE, nil, 5*time.Second)
	if clearKeyErr == nil && len(clearKeyResp.Parameters) >= 1 {
		clearKeyStatus := clearKeyResp.Parameters[0]
		if clearKeyStatus == 0x00 {
			if c.settings.LogCommands {
				fmt.Printf("[ember] Key table cleared successfully\n")
				os.Stdout.Sync()
			}
		} else if clearKeyStatus == 0x58 {
			if c.settings.LogCommands {
				fmt.Printf("[ember] WARNING: CLEAR_KEY_TABLE returned 0x58 (continuing anyway)\n")
				os.Stdout.Sync()
			}
		} else {
			if c.settings.LogErrors {
				fmt.Printf("[ember] WARNING: CLEAR_KEY_TABLE returned 0x%02X (continuing anyway)\n", clearKeyStatus)
				os.Stdout.Sync()
			}
		}
	} else if c.settings.LogErrors {
		fmt.Printf("[ember] WARNING: CLEAR_KEY_TABLE failed: %v (continuing anyway)\n", clearKeyErr)
		os.Stdout.Sync()
	}

	// Small delay before forming network to let device settle
	time.Sleep(500 * time.Millisecond)

	// Step 2: Form network with network parameters
	// Format: [extendedPanId:8 bytes][panId:uint16][radioTxPower:int8][radioChannel:uint8][joinMethod:uint8][nwkManagerId:uint16][nwkUpdateId:uint8][channels:uint32]
	networkParams := make([]byte, 0, 8+2+1+1+1+2+1+4)

	// Extended PAN ID (8 bytes, little-endian)
	extPanIDBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(extPanIDBytes, params.ExtendedPanID)
	networkParams = append(networkParams, extPanIDBytes...)

	// PAN ID (uint16, little-endian)
	panIDBytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(panIDBytes, params.PanID)
	networkParams = append(networkParams, panIDBytes...)

	// Radio TX Power (int8, use default: 3 = +3 dBm)
	networkParams = append(networkParams, 0x03)

	// Radio Channel (uint8)
	networkParams = append(networkParams, params.Channel)

	// Join Method (uint8, 0 = use MAC association, ignored for forming)
	networkParams = append(networkParams, 0x00)

	// NWK Manager ID (uint16, little-endian, 0x0000 for coordinator)
	networkParams = append(networkParams, 0x00, 0x00)

	// NWK Update ID (uint8, 0 for new network)
	networkParams = append(networkParams, 0x00)

	// Channels mask (uint32, little-endian, 0x07FFF800 = channels 11-26)
	// This is the standard 2.4GHz Zigbee channel mask
	channelsMask := uint32(0x07FFF800)
	channelsBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(channelsBytes, channelsMask)
	networkParams = append(networkParams, channelsBytes...)

	if c.settings.LogCommands {
		fmt.Printf("[ember] Sending FORM_NETWORK command...\n")
		os.Stdout.Sync()
	}

	// Use a longer timeout for FORM_NETWORK - the device may need time to process
	// especially when it's returning 0x58 for other commands
	// NOTE: Since network formation is asynchronous, the device may accept the command
	// but not send an immediate response. If the command times out, we'll treat it as
	// "command sent" and wait for the network to come up by polling NETWORK_STATE.
	formResp, err := c.sendEZSPCommand(EZSP_FORM_NETWORK, networkParams, 15*time.Second)
	var formStatus byte = 0xFF // Default to unknown status
	if err != nil {
		// Command timed out or failed - this might mean the device accepted it but didn't respond
		// (network formation is asynchronous). We'll proceed to poll for network state.
		if c.settings.LogErrors {
			fmt.Printf("[ember] WARNING: FORM_NETWORK command timed out or failed: %v\n", err)
			fmt.Printf("[ember] This may be normal - network formation is asynchronous.\n")
			fmt.Printf("[ember] Proceeding to poll NETWORK_STATE to check if network forms...\n")
			os.Stdout.Sync()
		}
		// Continue to polling below - don't return error yet
	} else if len(formResp.Parameters) >= 1 {
		formStatus = formResp.Parameters[0]
		// Status 0x00 = SUCCESS
		// Status 0x58 = TRANSMIT_BLOCKED - Some firmware versions return this for FORM_NETWORK even when
		// the command is accepted. The network formation proceeds asynchronously, so we treat 0x58 as
		// "accepted" and wait for the network to come up. This is a firmware quirk that we've
		// observed in practice - the device may form the network despite returning 0x58.
		if formStatus != 0x00 && formStatus != 0x58 {
			return fmt.Errorf("form network command failed with status 0x%02X", formStatus)
		}

		if c.settings.LogCommands {
			if formStatus == 0x58 {
				fmt.Printf("[ember] FORM_NETWORK returned 0x58 (treating as accepted, network formation may proceed asynchronously)\n")
			} else {
				fmt.Printf("[ember] Network formation initiated successfully\n")
			}
		}
	} else {
		// Response too short - treat as timeout/unknown
		if c.settings.LogErrors {
			fmt.Printf("[ember] WARNING: FORM_NETWORK response too short (treating as timeout, will poll for network)\n")
			os.Stdout.Sync()
		}
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Waiting for network to form (this may take several seconds)...\n")
		os.Stdout.Sync()
	}

	// Network formation is asynchronous - we need to wait for the network to come up
	// zigbee-herdsman waits for STACK_STATUS_NETWORK_UP callback, but also polls as fallback
	// Status values: 0x90 = NETWORK_UP (SLStatus), 0x15 = NETWORK_UP (EmberStatus), 0x02 = JOINED_NETWORK
	// NOTE: Network formation can take 10-60 seconds depending on device and firmware
	// zigbee-herdsman uses 10s timeout for Ember, but Z-Stack can take up to 60s
	// We use 45 seconds to be safe, especially when device returns 0x58
	timeout := 45 * time.Second
	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second
	lastPoll := time.Now()
	startTime := time.Now()

	if c.settings.LogCommands {
		fmt.Printf("[ember] Network formation is asynchronous - waiting for STACK_STATUS callback or polling NETWORK_STATE (timeout: %v)\n", timeout)
		os.Stdout.Sync()
	}

	for time.Now().Before(deadline) {
		elapsed := time.Since(startTime)

		// First, check for STACK_STATUS callback (preferred method)
		select {
		case status := <-c.stackStatusCh:
			if status == 0x90 || status == 0x15 { // NETWORK_UP
				if c.settings.LogCommands {
					fmt.Printf("[ember] Network is UP! (status: 0x%02X from callback, elapsed: %v)\n", status, elapsed.Round(time.Second))
					os.Stdout.Sync()
				}
				return nil // Network formation successful!
			} else if status == 0x91 { // NETWORK_DOWN
				return fmt.Errorf("network formation failed: device reported NETWORK_DOWN (0x91)")
			} else {
				if c.settings.LogCommands {
					fmt.Printf("[ember] STACK_STATUS callback: 0x%02X (waiting for NETWORK_UP...)\n", status)
					os.Stdout.Sync()
				}
				// Continue waiting for NETWORK_UP
			}
		case <-time.After(500 * time.Millisecond):
			// No callback received, poll NETWORK_STATE as fallback
			if time.Since(lastPoll) >= pollInterval {
				lastPoll = time.Now()
				stateResp, err := c.sendEZSPCommand(EZSP_NETWORK_STATE, nil, 2*time.Second)
				if err == nil && len(stateResp.Parameters) >= 1 {
					state := stateResp.Parameters[0]
					if state == 0x90 || state == 0x02 { // NETWORK_UP or JOINED_NETWORK
						if c.settings.LogCommands {
							fmt.Printf("[ember] Network is UP! (status: 0x%02X from polling, elapsed: %v)\n", state, elapsed.Round(time.Second))
							os.Stdout.Sync()
						}
						return nil // Network formation successful!
					} else if c.settings.LogCommands {
						remaining := time.Until(deadline)
						fmt.Printf("[ember] Polled NETWORK_STATE: 0x%02X (elapsed: %v, remaining: %v)\n",
							state, elapsed.Round(time.Second), remaining.Round(time.Second))
						os.Stdout.Sync()
					}
				} else if err != nil && c.settings.LogErrors {
					fmt.Printf("[ember] Error polling NETWORK_STATE: %v (continuing...)\n", err)
					os.Stdout.Sync()
				}
			}
		}
	}

	// Timeout - network didn't form
	elapsed := time.Since(startTime)
	return fmt.Errorf("timeout waiting for network to form after FORM_NETWORK (elapsed: %v, device returned 0x%02X)",
		elapsed.Round(time.Second), formStatus)
}

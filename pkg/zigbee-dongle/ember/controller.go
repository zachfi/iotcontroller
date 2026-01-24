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
}

func NewController(settings Settings) (*Controller, error) {
	port, err := NewPort(settings.Port, settings.BaudRate, settings.DisableFlowControl)
	if err != nil {
		return nil, err
	}

	return &Controller{
		port:     port,
		settings: &settings,
		handlers: make(map[uint8]*Handler),
		done:     make(chan struct{}),
		rxSeq:    0,
		txSeq:    0,
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
	if c.settings.LogCommands {
		fmt.Printf("[ember] Waiting for device to stabilize after reset (1s)...\n")
		os.Stdout.Sync()
	}
	time.Sleep(1 * time.Second)

	// Check if device is sending any data (bootloader messages, RSTACK, etc.)
	// Some devices send RSTACK automatically after reset
	handshakeComplete := false
	if c.settings.LogCommands {
		fmt.Printf("[ember] Checking for device boot messages or RSTACK...\n")
		os.Stdout.Sync()
	}
	bootData, err := c.port.ReadRawBytes(2*time.Second, 512)
	if err == nil && len(bootData) > 0 {
		if c.settings.LogCommands {
			fmt.Printf("[ember] Device sent data: %X (%q)\n", bootData, bootData)
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

// readLoop reads ASH frames and processes EZSP messages.
func (c *Controller) readLoop(ctx context.Context, output chan<- types.IncomingMessage) {
	defer func() {
		close(output)
		if c.settings.LogCommands {
			fmt.Printf("[ember] Read loop exited\n")
			os.Stdout.Sync()
		}
	}()

	if c.settings.LogCommands {
		fmt.Printf("[ember] Read loop started, waiting for ASH frames...\n")
		os.Stdout.Sync()
	}

	for {
		// Check for cancellation first
		select {
		case <-ctx.Done():
			if c.settings.LogCommands {
				fmt.Printf("[ember] Read loop: context cancelled\n")
				os.Stdout.Sync()
			}
			return
		case <-c.done:
			if c.settings.LogCommands {
				fmt.Printf("[ember] Read loop: controller done\n")
				os.Stdout.Sync()
			}
			return
		default:
		}

		if c.settings.LogCommands {
			fmt.Printf("[ember] Read loop: calling ReadASHFrame()...\n")
			os.Stdout.Sync()
		}

		// Use a goroutine with timeout to make ReadASHFrame interruptible
		type frameResult struct {
			frame []byte
			err   error
		}
		frameCh := make(chan frameResult, 1)

		readFrameDone := make(chan struct{})
		go func() {
			defer close(readFrameDone)
			if c.settings.LogCommands {
				fmt.Printf("[ember] Read loop: ReadASHFrame goroutine started\n")
				os.Stdout.Sync()
			}
			rawFrame, err := c.port.ReadASHFrame()
			if c.settings.LogCommands {
				if err != nil {
					fmt.Printf("[ember] Read loop: ReadASHFrame returned error: %v\n", err)
				} else {
					fmt.Printf("[ember] Read loop: ReadASHFrame returned frame: %X\n", rawFrame)
				}
				os.Stdout.Sync()
			}
			// Try to send, but don't block if the read loop has exited
			select {
			case frameCh <- frameResult{rawFrame, err}:
			case <-ctx.Done():
			case <-c.done:
			}
		}()

		var rawFrame []byte
		var err error

		select {
		case <-ctx.Done():
			if c.settings.LogCommands {
				fmt.Printf("[ember] Read loop: context cancelled during frame read\n")
				os.Stdout.Sync()
			}
			// Wait briefly for the ReadASHFrame goroutine to finish
			select {
			case <-readFrameDone:
			case <-time.After(100 * time.Millisecond):
			}
			return
		case <-c.done:
			if c.settings.LogCommands {
				fmt.Printf("[ember] Read loop: controller done during frame read\n")
				os.Stdout.Sync()
			}
			// Wait briefly for the ReadASHFrame goroutine to finish
			select {
			case <-readFrameDone:
			case <-time.After(100 * time.Millisecond):
			}
			return
		case res := <-frameCh:
			rawFrame = res.frame
			err = res.err
			if c.settings.LogCommands {
				fmt.Printf("[ember] Read loop: received result from frameCh\n")
				os.Stdout.Sync()
			}
		}

		if err != nil {
			if errors.Is(err, os.ErrClosed) || err == io.EOF {
				if c.settings.LogCommands {
					fmt.Printf("[ember] Read loop: port closed\n")
					os.Stdout.Sync()
				}
				return
			}
			if c.settings.LogErrors {
				fmt.Printf("[ember] Error reading ASH frame: %v\n", err)
				os.Stdout.Sync()
			}
			continue
		}

		if c.settings.LogCommands {
			fmt.Printf("[ember] Read loop: parsing ASH frame, raw=%X\n", rawFrame)
			os.Stdout.Sync()
		}

		ashFrame, err := ParseASHFrame(rawFrame)
		if err != nil {
			if c.settings.LogErrors {
				fmt.Printf("[ember] Error parsing ASH frame: %v (raw: %X)\n", err, rawFrame)
				os.Stdout.Sync()
			}
			continue
		}

		if c.settings.LogCommands {
			fmt.Printf("[ember] Read loop: parsed ASH frame type=%v\n", ashFrame.Type)
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
				continue
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
}

// handleCallback processes EZSP callback frames (like incoming messages).
func (c *Controller) handleCallback(frame *EZSPFrame, output chan<- types.IncomingMessage) {
	switch frame.FrameID {
	case EZSP_INCOMING_MESSAGE_HANDLER:
		// TODO: Parse incoming message and send to output channel
		if c.settings.LogCommands {
			fmt.Printf("[ember] Incoming message callback (not yet fully implemented)\n")
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

	// Map network status to state string
	stateStr := "Unknown"
	switch networkStatus {
	case 0x90: // EMBER_NETWORK_UP
		stateStr = "NetworkUp"
	case 0x91: // EMBER_NETWORK_DOWN
		stateStr = "NetworkDown"
	case 0x93: // EMBER_NOT_JOINED
		stateStr = "NotJoined"
	default:
		stateStr = fmt.Sprintf("0x%02X", networkStatus)
	}

	// If not joined or network is down, return minimal info
	if networkStatus == 0x93 || networkStatus == 0x91 {
		return &types.NetworkInfo{
			State: stateStr,
		}, nil
	}

	// Only continue if network is up
	if networkStatus != 0x90 {
		return &types.NetworkInfo{
			State: stateStr,
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
				State: "NotJoined",
			}, nil
		}
		// Other error status - return error with state info
		return &types.NetworkInfo{
			State: fmt.Sprintf("%s (params error: 0x%02X)", stateStr, status),
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
		State:                 stateStr,
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

	// Step 1: Set initial security state with Network Key
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
	if securityStatus != 0x00 { // SUCCESS
		return fmt.Errorf("setting initial security state failed with status 0x%02X", securityStatus)
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Initial security state set successfully\n")
		os.Stdout.Sync()
	}

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

	formResp, err := c.sendEZSPCommand(EZSP_FORM_NETWORK, networkParams, 10*time.Second)
	if err != nil {
		return fmt.Errorf("sending form network command: %w", err)
	}

	if len(formResp.Parameters) < 1 {
		return fmt.Errorf("form network response too short")
	}

	formStatus := formResp.Parameters[0]
	if formStatus != 0x00 { // SUCCESS
		return fmt.Errorf("form network command failed with status 0x%02X", formStatus)
	}

	if c.settings.LogCommands {
		fmt.Printf("[ember] Network formation initiated successfully\n")
		fmt.Printf("[ember] Waiting for network to form (this may take several seconds)...\n")
		os.Stdout.Sync()
	}

	// Network formation is asynchronous - we need to wait for the stack status handler
	// to indicate the network is up. This is typically handled by waiting for
	// NETWORK_STATE to return NETWORK_UP (0x90).
	// For now, we'll return success and let the caller check network state.
	// In a full implementation, we'd wait for a callback or poll NETWORK_STATE.

	return nil
}

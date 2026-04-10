package ember

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	// SkipForcedLeave: if true, do not send LEAVE before NETWORK_INIT (match zigbee-herdsman order).
	// Use when formation returns 0x58; herdsman does not send leave first.
	SkipForcedLeave bool
	LogCommands     bool
	LogErrors       bool
	Logger          *slog.Logger // optional; used for layer=ember logs when set
}

// Controller implements the Zigbee dongle interface using the Ember EZSP protocol (ASH framing).
// Logs use "layer" attribute "ember" for consistency with ZNP (layer=znp, layer=zigbee).
type Controller struct {
	port       *Port
	settings   *Settings
	log        *slog.Logger // layer=ember: startup, formation, EZSP, ASH
	handlers   map[uint8]*Handler
	handlerMux sync.Mutex
	done       chan struct{}
	readLoopWg sync.WaitGroup

	// ASH protocol state
	rxSeq uint8
	txSeq uint8

	stackStatusCh chan byte                  // for waiting for NETWORK_UP after FORM_NETWORK
	joinEvents    chan types.DeviceJoinEvent // device join events from TRUST_CENTER_JOIN_HANDLER

	// ZDO response waiters: clusterID (response cluster) → buffered channel of raw ZDP payloads
	zdoWaitersMux sync.Mutex
	zdoWaiters    map[uint16]chan []byte

	// ASH retransmit: components of last DATA frame for NAK-triggered retransmit.
	// We store control+ezspData separately so we can rebuild with reTx=1.
	lastFrameControl byte
	lastFrameEZSP    []byte
	lastDataMux      sync.Mutex
}

func NewController(settings Settings) (*Controller, error) {
	logger := settings.Logger
	if logger == nil {
		logger = slog.Default()
	}
	emberLog := logger.With("layer", "ember")

	port, err := NewPort(settings.Port, settings.BaudRate, settings.DisableFlowControl)
	if err != nil {
		return nil, err
	}
	if settings.DisableFlowControl {
		emberLog.Info("flow control disabled (RTS/CTS off)")
	} else {
		emberLog.Info("flow control enabled (RTS/CTS on)")
	}

	return &Controller{
		port:          port,
		settings:      &settings,
		log:           emberLog,
		handlers:      make(map[uint8]*Handler),
		done:          make(chan struct{}),
		rxSeq:         0,
		txSeq:         0,
		stackStatusCh: make(chan byte, 10),
		joinEvents:    make(chan types.DeviceJoinEvent, 10),
		zdoWaiters:    make(map[uint16]chan []byte),
	}, nil
}

func (c *Controller) Close() error {
	c.log.Debug("close called")

	// Signal that we're closing
	select {
	case <-c.done:
		// Already closed
		c.log.Debug("already closed")
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
	c.log.Debug("closing port (non-blocking)")

	portCloseDone := make(chan error, 1)
	go func() {
		portCloseDone <- c.port.Close()
	}()

	select {
	case err := <-portCloseDone:
		if err != nil {
			c.log.Warn("error closing port", slog.Any("error", err))
		} else {
			c.log.Debug("port closed")
		}
	case <-time.After(50 * time.Millisecond):
		c.log.Debug("port close timed out (continuing - read loop already exited)")
	}
	c.log.Debug("close complete")

	return nil
}

// Start initializes the controller and returns a channel of incoming messages.
func (c *Controller) Start(ctx context.Context) (<-chan types.IncomingMessage, error) {
	c.log.Info("starting controller", slog.String("port", c.settings.Port))

	// Step 0: Hardware reset (similar to ZNP)
	c.log.Debug("performing hardware reset")
	if err := c.port.ResetDevice(); err != nil {
		c.log.Warn("hardware reset failed (continuing)", slog.Any("error", err))
	}

	// Give device time to initialize after reset
	// EZSP devices, especially USB-serial bridges, may need more time
	// Devices with ESP32 bridges (like Sonoff Dongle Max) may send boot messages first
	if c.settings.LogCommands {
		c.log.Debug("waiting for device to stabilize after reset (1s)...")
	}
	time.Sleep(1 * time.Second)

	// Check if device is sending any data (bootloader messages, RSTACK, etc.)
	// Some devices send RSTACK automatically after reset
	// Devices with ESP32 bridges may send boot messages - we need to drain those
	// and wait for the EFR32MG24 to become active
	handshakeComplete := false
	if c.settings.LogCommands {
		c.log.Debug("checking for device boot messages or RSTACK...")
	}

	// Read for longer to catch ESP32 boot messages and wait for EFR32MG24 to become active
	// Sonoff Dongle Max may send ESP32 boot messages before the EFR32MG24 is ready
	bootData, err := c.port.ReadRawBytes(5*time.Second, 2048) // Increased timeout and buffer size
	if err == nil && len(bootData) > 0 {
		// Check if this looks like ESP32 boot messages (text, not binary ASH frames).
		// Null bytes (0x00) are treated as non-text: they're a CP2102N line artifact
		// on DTR toggle and must not trigger the ESP32 detection path.
		isText := true
		for _, b := range bootData {
			if b < 0x20 && b != 0x0A && b != 0x0D && b != 0x09 {
				isText = false
				break
			}
		}

		if c.settings.LogCommands {
			if isText && len(bootData) > 20 {
				// Likely ESP32 boot messages - show first/last part
				preview := string(bootData)
				if len(preview) > 100 {
					preview = preview[:50] + "..." + preview[len(preview)-50:]
				}
				c.log.Debug("device sent text data (likely ESP32 boot messages)", slog.String("preview", preview))
			} else {
				c.log.Debug("device sent data", slog.String("raw", fmt.Sprintf("%X", bootData)), slog.String("text", fmt.Sprintf("%q", bootData)))
			}
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
				c.log.Debug("detected ESP32 boot messages - waiting for EFR32MG24 to become active...")
				c.log.Debug("note: Sonoff Dongle Max may need ESP32 to finish booting before EFR32MG24 is accessible")
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
						c.log.Debug("still receiving text data (ESP32 may still be booting)")
					} else {
						c.log.Debug("received binary data (possible ASH frames)")
					}
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
					c.log.Debug("found ASH frames after ESP32 boot completed")
				} else if c.settings.LogCommands && !hasFlag {
					c.log.Debug("wARNING: No ASH frames detected after waiting. EFR32MG24 may not be active.")
					c.log.Debug("this device may need web console configuration or different firmware.")
				}
			}
		}

		if hasFlag {
			// Try to parse as ASH frame - might be RSTACK
			if c.settings.LogCommands {
				c.log.Debug("data contains ASH flag bytes, attempting to parse...")
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
								c.log.Debug("found potential ASH frame (flag-delimited)", slog.String("raw", fmt.Sprintf("%X", frameData)))
							}

							ashFrame, parseErr := ParseASHFrame(frameData)
							if parseErr == nil {
								if c.settings.LogCommands {
									c.log.Debug("parsed frame type", slog.String("type", fmt.Sprintf("%v", ashFrame.Type)))
								}

								if ashFrame.Type == ASH_FRAME_RSTACK {
									if c.settings.LogCommands {
										c.log.Debug("device sent RSTACK automatically! Handshake already complete.")
										if len(ashFrame.Data) >= 2 {
											version := ashFrame.Data[0]
											resetCode := ashFrame.Data[1]
											c.log.Debug("RSTACK version", slog.String("version", fmt.Sprintf("0x%02X", version)), slog.String("reset_code", fmt.Sprintf("0x%02X", resetCode)))
											if version != 0x02 {
												c.log.Debug("RSTACK version mismatch", slog.String("expected", "0x02"), slog.String("got", fmt.Sprintf("0x%02X", version)))
											}
										}
									}
									// Device already sent RSTACK, we're connected
									// Verify version (should be 0x02 for ASH version 2)
									if len(ashFrame.Data) >= 1 && ashFrame.Data[0] != 0x02 {
										if c.settings.LogErrors {
											c.log.Warn("RSTACK version mismatch", slog.String("expected", "0x02"), slog.String("got", fmt.Sprintf("0x%02X", ashFrame.Data[0])))
										}
										// Continue anyway - might still work
									}
									// RSTACK resets the ASH session, so reset sequence numbers
									c.txSeq = 0
									c.rxSeq = 0
									if c.settings.LogCommands {
										c.log.Debug("reset sequence numbers after RSTACK: txSeq=0, rxSeq=0")
									}
									handshakeComplete = true
									break
								} else if ashFrame.Type == ASH_FRAME_RST {
									if c.settings.LogCommands {
										c.log.Debug("device sent RST automatically")
									}
									// Device sent RST, we should respond with RSTACK or send our own RST
								}
							} else {
								if c.settings.LogErrors {
									c.log.Warn("failed to parse frame", slog.Any("error", parseErr))
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
								c.log.Debug("found potential RSTACK frame (C1-based)", slog.String("raw", fmt.Sprintf("%X", frameData)))
							}

							ashFrame, parseErr := ParseASHFrame(frameData)
							if parseErr == nil && ashFrame.Type == ASH_FRAME_RSTACK {
								if c.settings.LogCommands {
									c.log.Debug("device sent RSTACK automatically! Handshake already complete.")
									if len(ashFrame.Data) >= 2 {
										version := ashFrame.Data[0]
										resetCode := ashFrame.Data[1]
										c.log.Debug("RSTACK version", slog.String("version", fmt.Sprintf("0x%02X", version)), slog.String("reset_code", fmt.Sprintf("0x%02X", resetCode)))
										if version != 0x02 {
											c.log.Debug("RSTACK version mismatch", slog.String("expected", "0x02"), slog.String("got", fmt.Sprintf("0x%02X", version)))
										}
									}
								}
								// Verify version (should be 0x02 for ASH version 2)
								if len(ashFrame.Data) >= 1 && ashFrame.Data[0] != 0x02 {
									if c.settings.LogErrors {
										c.log.Warn("RSTACK version mismatch", slog.String("expected", "0x02"), slog.String("got", fmt.Sprintf("0x%02X", ashFrame.Data[0])))
									}
									// Continue anyway - might still work
								}
								// RSTACK resets the ASH session, so reset sequence numbers
								c.txSeq = 0
								c.rxSeq = 0
								if c.settings.LogCommands {
									c.log.Debug("reset sequence numbers after RSTACK: txSeq=0, rxSeq=0")
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
					c.log.Debug("wARNING: Device appears to be in bootloader mode!")
				}
			}
		}
	} else {
		if c.settings.LogCommands {
			c.log.Debug("no boot messages detected (device may already be initialized)")
		}
	}

	// Step 1: Perform RST/RSTACK handshake (if not already complete)
	// If device sent RSTACK automatically, the handshake is already done
	// and we should NOT send another RST (device won't respond)
	if !handshakeComplete {
		if c.settings.LogCommands {
			c.log.Debug("performing ASH handshake...")
			// Force flush
		}
		if err := c.performRSTHandshake(); err != nil {
			return nil, fmt.Errorf("ASH handshake: %w", err)
		}
		if c.settings.LogCommands {
			c.log.Debug("aSH handshake complete")
		}
	} else {
		if c.settings.LogCommands {
			c.log.Debug("aSH handshake already complete (device sent RSTACK automatically)")
			c.log.Debug("skipping RST/RSTACK handshake - proceeding directly to version command")
		}
	}

	// Drain any remaining data from the port after handshake
	// This ensures the read loop starts with a clean state
	// Note: We use a timeout-based drain to avoid blocking
	// IMPORTANT: We read RSTACK using ReadRawBytes (bypassing bufio.Reader),
	// so we need to ensure the bufio.Reader buffer is also clear
	if c.settings.LogCommands {
		c.log.Debug("draining any remaining data from port (non-blocking)...")
		buffered := c.port.Buffered()
		if buffered > 0 {
			c.log.Debug("bufio.Reader has buffered bytes", slog.Int("bytes", buffered))
		}
	}
	if err := c.port.Drain(); err != nil {
		if c.settings.LogErrors {
			c.log.Warn("failed to drain port", slog.Any("error", err))
		}
	}

	// CRITICAL: Synchronize to a frame boundary before starting the read loop
	// This prevents "starting mid-frame" messages by ensuring we start reading
	// at the beginning of a frame (FLAG byte)
	if c.settings.LogCommands {
		c.log.Debug("synchronizing to frame boundary (waiting for FLAG byte)...")
	}
	synced, err := c.port.SyncToFrameBoundary(2 * time.Second)
	if err != nil && err != io.ErrNoProgress {
		if c.settings.LogErrors {
			c.log.Warn("failed to sync to frame boundary (continuing)", slog.Any("error", err))
		}
	} else if synced {
		if c.settings.LogCommands {
			c.log.Debug("synchronized to frame boundary")
		}
	} else {
		if c.settings.LogCommands {
			c.log.Debug("no FLAG byte found during sync (device may be idle, continuing anyway)")
		}
	}

	if c.settings.LogCommands {
		c.log.Debug("port drain complete")
	}

	// Step 2: Start read loop FIRST so we can receive responses
	// This must be started before sending any commands
	output := make(chan types.IncomingMessage)
	if c.settings.LogCommands {
		c.log.Debug("starting read loop goroutine...")
	}
	c.readLoopWg.Add(1)
	go func() {
		defer c.readLoopWg.Done()
		c.readLoop(ctx, output)
	}()

	// Give the read loop a moment to start
	time.Sleep(100 * time.Millisecond)
	if c.settings.LogCommands {
		c.log.Debug("read loop should be running now")
	}

	// Wait a bit longer after RSTACK before sending first command
	// Some devices need time to fully initialize after handshake
	if c.settings.LogCommands {
		c.log.Debug("waiting additional 500ms after RSTACK before sending version command...")
	}
	time.Sleep(500 * time.Millisecond)

	// Step 3: Send version command to verify communication
	if c.settings.LogCommands {
		c.log.Debug("sending version command...")
	}

	// EZSP version command always uses legacy 3-byte format (no matter what protocol version).
	// We request protocol version 13 (0x0D) directly, matching zigbee-herdsman's approach.
	// The NCP responds with its actual version; if >= 8, we switch to extended 5-byte format.
	versionFrame := &EZSPFrame{
		Sequence:   0, // Legacy format always uses sequence 0 for VERSION
		Control:    EZSP_FRAME_CONTROL_COMMAND,
		FrameID:    EZSP_VERSION,
		Parameters: []byte{0x0D}, // Request protocol 13 (EmberZNet 7.x)
	}

	if c.settings.LogCommands {
		c.log.Debug("version command (legacy format, sequence=0)")
	}

	// Register handler using sequence 0 for legacy format
	handler := c.registerHandler(0, 5*time.Second)

	ezspData := SerializeEZSPFrame(versionFrame)
	// DATA frame control: FrameNum (3 bits) + ReTx (1 bit) + AckNum (3 bits)
	// FrameNum = txSeq, AckNum = rxSeq
	// Note: ReTx bit (bit 3) should be 0 for first transmission
	control := byte((c.txSeq << 4) | (c.rxSeq & 0x07))
	c.log.Debug("building DATA frame", slog.Int("tx_seq", int(c.txSeq)), slog.Int("rx_seq", int(c.rxSeq)), slog.String("control", fmt.Sprintf("0x%02X", control)))
	ashDataFrame := buildASHDataFrame(control, ezspData)
	// Increment txSeq after building frame (for next frame)
	c.txSeq = (c.txSeq + 1) % 8

	c.log.Debug("version command frame", slog.String("raw", fmt.Sprintf("%X", ashDataFrame)))
	n, err := c.port.WriteBytes(ashDataFrame)
	if err != nil {
		c.removeHandler(versionFrame.Sequence, handler)
		return nil, fmt.Errorf("sending version command: %w", err)
	}

	c.log.Debug("version command sent", slog.Int("bytes", n))

	// Give device a moment to process and send ACK/response
	// NOTE: Do NOT use ReadRawBytes here - it reads directly from the connection
	// and would consume data that the read loop needs! Let the read loop handle all incoming data.
	// Give it a bit more time - some devices are slow to respond
	time.Sleep(200 * time.Millisecond)

	if c.settings.LogCommands {
		// Check if read loop has received anything (non-blocking check)
		c.log.Debug("checking read loop status (should be waiting for frames)...")
	}

	// Read version response using handler (read loop will route it)
	if c.settings.LogCommands {
		c.log.Debug("waiting for version response (timeout: 5s)...")
	}
	versionResponse, err := handler.Receive()
	if err != nil {
		return nil, fmt.Errorf("reading version response: %w", err)
	}

	version, err := ParseVersionResponse(versionResponse.Parameters)
	if err != nil {
		return nil, fmt.Errorf("parsing version response: %w", err)
	}

	c.log.Info("EZSP version", slog.Int("protocol", int(version.ProtocolVersion)), slog.Int("stack_type", int(version.StackType)), slog.Int("stack_version", int(version.StackVersion)))

	// Give the NCP a moment to finish any pending transmissions (callbacks, status frames)
	// before we start sending configuration commands. The NCP may queue additional frames
	// after VERSION (e.g., STACK_STATUS callbacks) that arrive ~simultaneously.
	time.Sleep(500 * time.Millisecond)

	// Step 4: Initialize NCP configuration (setConfigurationValue + setPolicy + addEndpoint).
	// Must be done after VERSION and before any network operations to prevent 0x58.
	if err := c.initEzsp(); err != nil {
		return nil, fmt.Errorf("EZSP initialization: %w", err)
	}

	return output, nil
}

// performRSTHandshake performs the ASH RST/RSTACK handshake.
func (c *Controller) performRSTHandshake() error {
	if c.settings.LogCommands {
		c.log.Debug("performRSTHandshake: starting...")
	}

	// First, check if device is sending ANY data at all (raw bytes test)
	if c.settings.LogCommands {
		c.log.Debug("testing if device is responsive (reading raw bytes)...")
	}
	rawBytes, err := c.port.ReadRawBytes(200*time.Millisecond, 256)
	if err == nil && len(rawBytes) > 0 {
		c.log.Debug("device is sending data", slog.String("raw", fmt.Sprintf("%X", rawBytes)), slog.String("text", fmt.Sprintf("%q", rawBytes)))
	} else {
		if c.settings.LogCommands {
			c.log.Debug("no data from device yet (this is OK, device may need RST first)")
		}
	}

	// Drain any pending data first (non-blocking)
	if c.settings.LogCommands {
		c.log.Debug("draining any pending data from serial port...")
	}
	// Drain is non-blocking (reads with timeout), so this should be safe
	if err := c.port.Drain(); err != nil {
		if c.settings.LogErrors {
			c.log.Warn("failed to drain port", slog.Any("error", err))
		}
	}
	if c.settings.LogCommands {
		c.log.Debug("drain complete")
	}

	// Some devices (like Sonoff Dongle-E) may send an RST frame automatically
	// Check for device-initiated RST first (non-blocking)
	if c.settings.LogCommands {
		c.log.Debug("checking for device-initiated RST frame (timeout: 500ms)...")
	}
	rawFrame, err := c.port.ReadASHFrameWithTimeout(500 * time.Millisecond)
	if c.settings.LogCommands {
		if err != nil {
			c.log.Debug("no device-initiated RST received", slog.Any("error", err))
		} else {
			c.log.Debug("received frame from device", slog.String("raw", fmt.Sprintf("%X", rawFrame)))
		}
	}
	if err == nil {
		ashFrame, parseErr := ParseASHFrame(rawFrame)
		if parseErr == nil && ashFrame.Type == ASH_FRAME_RST {
			c.log.Debug("device sent RST automatically", slog.String("raw", fmt.Sprintf("%X", rawFrame)))
		} else if parseErr != nil {
			if c.settings.LogErrors {
				c.log.Warn("received data but not valid ASH frame", slog.Any("error", parseErr), slog.String("raw", fmt.Sprintf("%X", rawFrame)))
			}
		} else {
			c.log.Debug("device sent frame", slog.String("type", fmt.Sprintf("%v", ashFrame.Type)))
		}
	} else if err != io.ErrNoProgress {
		if c.settings.LogCommands {
			c.log.Debug("no device-initiated RST (this is normal)")
		}
	}

	// Send our own RST frame to initiate handshake
	// (Even if device sent RST, we send our own to establish the session)
	rst := buildRSTFrame()
	c.log.Debug("sending RST frame", slog.String("raw", fmt.Sprintf("%X", rst)))
	if _, err := c.port.WriteBytes(rst); err != nil {
		return fmt.Errorf("sending RST frame: %w", err)
	}

	// Wait for RSTACK
	if c.settings.LogCommands {
		c.log.Debug("waiting for RSTACK response (timeout: 5s)...")
	}
	rawFrame, err = c.port.ReadASHFrameWithTimeout(5 * time.Second)
	if err != nil {
		if c.settings.LogErrors {
			c.log.Warn("failed to read RSTACK", slog.Any("error", err))
			// Try reading raw bytes one more time to see if device is sending anything
			rawBytes, readErr := c.port.ReadRawBytes(1*time.Second, 256)
			if readErr == nil && len(rawBytes) > 0 {
				c.log.Warn("device sent data but not ASH frame", slog.String("raw", fmt.Sprintf("%X", rawBytes)), slog.String("text", fmt.Sprintf("%q", rawBytes)))
			} else {
				c.log.Debug("no data received from device - device appears silent")
			}
		}
		return fmt.Errorf("reading RSTACK: %w", err)
	}

	c.log.Debug("received frame", slog.String("raw", fmt.Sprintf("%X", rawFrame)))

	ashFrame, err := ParseASHFrame(rawFrame)
	if err != nil {
		if c.settings.LogErrors {
			c.log.Warn("failed to parse frame", slog.Any("error", err), slog.String("raw", fmt.Sprintf("%X", rawFrame)))
		}
		return fmt.Errorf("parsing RSTACK frame: %w", err)
	}

	c.log.Debug("parsed frame type", slog.String("type", fmt.Sprintf("%v", ashFrame.Type)))

	if ashFrame.Type != ASH_FRAME_RSTACK {
		return fmt.Errorf("expected RSTACK, got %v", ashFrame.Type)
	}

	// RSTACK resets the ASH session, so reset sequence numbers
	c.txSeq = 0
	c.rxSeq = 0
	if c.settings.LogCommands {
		c.log.Debug("reset sequence numbers after RSTACK: txSeq=0, rxSeq=0")
	}

	return nil
}

// resetASHSession sends RST to the NCP to recover a stuck ASH session.
// The read loop handles the resulting RSTACK and resets sequence numbers.
// This is safe to call while the read loop is running.
func (c *Controller) resetASHSession() {
	rst := buildRSTFrame()
	c.log.Warn("resetting ASH session (sending RST to NCP)")
	if _, err := c.port.WriteBytes(rst); err != nil {
		c.log.Warn("failed to send RST for ASH reset", slog.Any("error", err))
	}
	// Give the NCP time to process RST and send RSTACK (handled by read loop).
	time.Sleep(500 * time.Millisecond)
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
			c.log.Debug("read loop exited")
		}
	}()

	if c.settings.LogCommands {
		c.log.Debug("read loop started (continuous read architecture)")
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
				c.log.Debug("read loop: context cancelled")
			}
			// Wait for parser and reader to finish
			<-parserDone
			<-byteReaderDone
			return
		case <-c.done:
			if c.settings.LogCommands {
				c.log.Debug("read loop: controller done")
			}
			// Wait for parser and reader to finish
			<-parserDone
			<-byteReaderDone
			return
		case rawFrame, ok := <-frameCh:
			if !ok {
				// Parser closed channel (error or done)
				if c.settings.LogCommands {
					c.log.Debug("frame parser closed channel")
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
		c.log.Debug("byte reader started (chunk-based continuous read)")
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
				c.log.Debug("byte reader: port closed")
				return
			}
			c.log.Warn("byte reader error", slog.Any("error", err))
			// Continue reading on non-fatal errors
			continue
		}

		if n == 0 {
			continue
		}

		c.log.Debug("byte reader: received chunk", slog.Int("bytes", n), slog.String("raw", fmt.Sprintf("%X", chunkBuf[:n])))

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
		c.log.Debug("frame parser started")
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
					// Reuse the trailing FLAG as the leading FLAG of the next frame.
					// Per ASH spec, a single FLAG byte serves as both the end of one
					// frame and the start of the next (inter-frame flag sharing).
					currentFrame = []byte{b}
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
								c.log.Warn("frame parser: discarded bytes before FLAG (could not reconstruct)", slog.Int("bytes", len(garbageBuffer)))
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
							c.log.Warn("frame parser: garbage buffer too large, clearing", slog.Int("bytes", len(garbageBuffer)))
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
	c.log.Debug("handling ASH frame", slog.String("raw", fmt.Sprintf("%X", rawFrame)))

	ashFrame, err := ParseASHFrame(rawFrame)
	if err != nil {
		if c.settings.LogErrors {
			c.log.Warn("error parsing ASH frame", slog.Any("error", err), slog.String("raw", fmt.Sprintf("%X", rawFrame)))
		}
		return
	}

	c.log.Debug("parsed ASH frame", slog.String("type", fmt.Sprintf("%v", ashFrame.Type)))

	switch ashFrame.Type {
	case ASH_FRAME_DATA:
		// Verify CRC of received frame (diagnose CRC algorithm correctness).
		// The received raw frame (unescaped, without flags) has: [control][data...][crcHi][crcLo]
		unescapedRaw := unescapeASHFrame(rawFrame[1 : len(rawFrame)-1]) // strip flags, unescape
		if len(unescapedRaw) >= 3 {
			expectedCRC := binary.BigEndian.Uint16(unescapedRaw[len(unescapedRaw)-2:])
			computedCRC := crcCCITT(unescapedRaw[:len(unescapedRaw)-2])
			if computedCRC != expectedCRC && c.settings.LogErrors {
				c.log.Warn("received DATA frame CRC mismatch", slog.String("computed", fmt.Sprintf("0x%04X", computedCRC)), slog.String("expected", fmt.Sprintf("0x%04X", expectedCRC)))
			} else if c.settings.LogCommands {
				c.log.Debug("received DATA frame CRC ok", slog.String("crc", fmt.Sprintf("0x%04X", computedCRC)))
			}
		}
		c.log.Debug("received DATA frame", slog.Int("frame_num", int(ashFrame.FrameNum)), slog.Int("ack_num", int(ashFrame.AckNum)), slog.String("raw", fmt.Sprintf("%X", rawFrame)))

		// When we receive a DATA frame, we MUST send an ACK frame
		// Update our expected receive sequence if frame is in sequence
		if ashFrame.FrameNum == c.rxSeq {
			c.rxSeq = (c.rxSeq + 1) % 8
		}

		// Sync our txSeq to match the NCP's AckNum. The NCP's AckNum is the
		// FrameNum it expects from us next. If the NCP has pre-ACKed frames
		// beyond our current txSeq (e.g., due to a state reset or mismatch),
		// advance txSeq to prevent sending frames the NCP will silently discard.
		if ashFrame.AckNum != c.txSeq {
			c.log.Debug("syncing tx sequence", slog.Int("from", int(c.txSeq)), slog.Int("to", int(ashFrame.AckNum)))
			c.txSeq = ashFrame.AckNum
		}

		// Send ACK frame immediately (ackNum is the next frame we expect)
		ackFrame := buildASHACKFrame(c.rxSeq)
		c.log.Debug("sending ACK", slog.Int("ack_num", int(c.rxSeq)), slog.String("frame", fmt.Sprintf("%X", ackFrame)))
		if _, err := c.port.WriteBytes(ackFrame); err != nil {
			if c.settings.LogErrors {
				c.log.Warn("failed to send ACK", slog.Any("error", err))
			}
		}

		// Derandomize the Data Field (XOR with pseudo-random sequence to restore original)
		derandomizedData := derandomizeData(ashFrame.Data)

		// Detect EZSP frame format: check frameControlHi (byte[2]) & 0x03.
		// If == EZSP_EXTENDED_FRAME_FORMAT_VERSION (0x01) → extended 5-byte header.
		// Otherwise → legacy 3-byte header (VERSION response only).
		// After VERSION handshake, NCP switches to extended format for all frames.
		var ezspFrame *EZSPFrame
		if len(derandomizedData) >= 5 && derandomizedData[2]&0x03 == EZSP_EXTENDED_FRAME_FORMAT_VERSION {
			ezspFrame, err = ParseEZSPFrameExtended(derandomizedData)
		} else {
			ezspFrame, err = ParseEZSPFrame(derandomizedData)
		}
		if err != nil {
			if c.settings.LogErrors {
				c.log.Warn("error parsing EZSP frame", slog.Any("error", err), slog.String("data", fmt.Sprintf("%X", ashFrame.Data)), slog.String("derandomized", fmt.Sprintf("%X", derandomizedData)))
			}
			return
		}

		c.log.Debug("parsed EZSP frame", slog.Int("sequence", int(ezspFrame.Sequence)), slog.String("control", fmt.Sprintf("0x%02X", ezspFrame.Control)), slog.String("frame_id", fmt.Sprintf("0x%04X", ezspFrame.FrameID)), slog.Int("params_len", len(ezspFrame.Parameters)))

		// Route frame: NCP→host frames (responses and async callbacks) have direction bit set.
		// Responses match a pending command handler by sequence number.
		// Async callbacks have the async-callback flag (bit 4 of fcLo) set; they don't match handlers.
		if ezspFrame.Control == EZSP_FRAME_CONTROL_RESPONSE {
			// Check async-callback flag: bit 4 of frameControlLo (byte[1]).
			// Async callbacks should go directly to handleCallback even if a seq happens to match.
			isAsyncCallback := derandomizedData[1]&0x10 != 0

			c.handlerMux.Lock()
			handler := c.handlers[ezspFrame.Sequence]
			var availableSeqs []uint8
			if handler != nil && !isAsyncCallback {
				delete(c.handlers, ezspFrame.Sequence)
			} else if c.settings.LogCommands && handler == nil {
				availableSeqs = make([]uint8, 0, len(c.handlers))
				for seq := range c.handlers {
					availableSeqs = append(availableSeqs, seq)
				}
			}
			c.handlerMux.Unlock()

			if handler != nil && !isAsyncCallback {
				c.log.Debug("found handler for sequence", slog.Int("sequence", int(ezspFrame.Sequence)), slog.String("frame_id", fmt.Sprintf("0x%04X", ezspFrame.FrameID)))
				handler.fulfill(ezspFrame)
			} else {
				if c.settings.LogCommands {
					if isAsyncCallback {
						c.log.Debug("async callback", slog.String("frame_id", fmt.Sprintf("0x%04X", ezspFrame.FrameID)), slog.String("params", fmt.Sprintf("%X", ezspFrame.Parameters)))
					} else {
						c.log.Debug("no handler for sequence", slog.Int("sequence", int(ezspFrame.Sequence)), slog.String("frame_id", fmt.Sprintf("0x%04X", ezspFrame.FrameID)), slog.Any("available", availableSeqs))
					}
				}
				c.handleCallback(ezspFrame, output)
			}
		} else {
			// Command direction from NCP (shouldn't happen, but route to callback just in case)
			c.log.Debug("unexpected command-direction frame from NCP", slog.String("frame_id", fmt.Sprintf("0x%04X", ezspFrame.FrameID)))
			c.handleCallback(ezspFrame, output)
		}

	case ASH_FRAME_ACK:
		// Device acknowledged our DATA frame
		c.log.Debug("received ACK", slog.Int("ack_num", int(ashFrame.AckNum)), slog.Int("expected_next", int((c.txSeq-1)%8)))
		// ACK indicates device received our frame successfully
		// The ackNum tells us which frame the device expects next
		// We don't need to update txSeq here - it's already incremented when we sent the frame

	case ASH_FRAME_NAK:
		// Device rejected our DATA frame — retransmit with reTx=1.
		// Per ASH spec, the retransmit bit (bit 3 of control) must be set.
		// ackNum = next frame the NCP expects; we retransmit our last frame.
		c.lastDataMux.Lock()
		retransmitControl := c.lastFrameControl | 0x08 // set reTx=1
		retransmitEZSP := c.lastFrameEZSP
		c.lastDataMux.Unlock()
		if retransmitEZSP != nil {
			// Brief delay before retransmit to let the NCP recover from the NAK condition.
			time.Sleep(100 * time.Millisecond)
			retransmitFrame := buildASHDataFrame(retransmitControl, retransmitEZSP)
			n, err := c.port.WriteBytes(retransmitFrame)
			if c.settings.LogErrors {
				if err != nil {
					c.log.Warn("NAK retransmit failed", slog.Int("bytes_written", n), slog.Any("error", err))
				} else {
					c.log.Warn("received NAK, retransmitted", slog.Int("ack_num", int(ashFrame.AckNum)), slog.Int("bytes", n), slog.String("frame", fmt.Sprintf("%X", retransmitFrame)))
				}
			}
		} else if c.settings.LogErrors {
			c.log.Warn("received NAK but no frame to retransmit", slog.Int("ack_num", int(ashFrame.AckNum)))
		}

	case ASH_FRAME_RSTACK:
		// NCP sent RSTACK — this means the ASH session has been reset (either by our RST
		// or by the NCP spontaneously). Reset our sequence numbers to match.
		c.txSeq = 0
		c.rxSeq = 0
		c.log.Info("ASH session reset (received RSTACK)", slog.Int("tx_seq", 0), slog.Int("rx_seq", 0))

	case ASH_FRAME_ERROR:
		// Device error
		if c.settings.LogErrors {
			c.log.Debug("received ERROR frame")
		}
	}
}

// handleCallback processes EZSP callback frames (like incoming messages).
func (c *Controller) handleCallback(frame *EZSPFrame, output chan<- types.IncomingMessage) {
	switch frame.FrameID {
	case EZSP_INCOMING_MESSAGE_HANDLER:
		// incomingMessageHandler parameters (AN706):
		//   msgType(1) profileId(2LE) clusterId(2LE) srcEndpoint(1) dstEndpoint(1)
		//   options(2LE) groupId(2LE) sequence(1) lastHopLqi(1) lastHopRssi(1)
		//   sender(2LE) bindingIndex(1) addressIndex(1) messageLength(1) message[...]
		const minLen = 19 // bytes before message payload
		if len(frame.Parameters) < minLen {
			if c.settings.LogErrors {
				c.log.Warn("INCOMING_MESSAGE_HANDLER too short", slog.Int("bytes", len(frame.Parameters)))
			}
			return
		}
		p := frame.Parameters
		// p[0]: msgType; p[1:3]: profileId; p[3:5]: clusterId; p[5]: srcEndpoint
		// p[6]: dstEndpoint; p[7:9]: options; p[9:11]: groupId; p[11]: seq
		// p[12]: lastHopLqi; p[13]: lastHopRssi; p[14:16]: sender; p[16]: bindingIndex
		// p[17]: addressIndex; p[18]: messageLength; p[19+]: message
		profileID := binary.LittleEndian.Uint16(p[1:3])
		clusterID := binary.LittleEndian.Uint16(p[3:5])
		srcEndpoint := p[5]
		dstEndpoint := p[6]
		lqi := p[12]
		sender := binary.LittleEndian.Uint16(p[14:16])
		msgLen := int(p[18])
		if len(frame.Parameters) < minLen+msgLen {
			if c.settings.LogErrors {
				c.log.Warn("INCOMING_MESSAGE_HANDLER message truncated", slog.Int("expected", minLen+msgLen), slog.Int("got", len(frame.Parameters)))
			}
			return
		}
		payload := append([]byte(nil), p[minLen:minLen+msgLen]...)

		// ZDP responses (profile 0x0000, cluster 0x8xxx): route to ZDO waiter if registered.
		if profileID == 0x0000 && clusterID >= 0x8000 {
			c.zdoWaitersMux.Lock()
			ch, ok := c.zdoWaiters[clusterID]
			c.zdoWaitersMux.Unlock()
			if ok {
				select {
				case ch <- payload:
				default:
				}
				return
			}
		}

		msg := types.IncomingMessage{
			Source: types.Address{
				Mode:  types.AddressModeNWK,
				Short: sender,
			},
			SourceEndpoint:      srcEndpoint,
			DestinationEndpoint: dstEndpoint,
			ClusterID:           clusterID,
			LinkQuality:         lqi,
			Data:                payload,
		}
		select {
		case output <- msg:
		default:
			if c.settings.LogErrors {
				c.log.Warn("incoming message channel full, dropping message")
			}
		}

	case EZSP_TRUST_CENTER_JOIN_HANDLER:
		// trustCenterJoinHandler parameters (AN706):
		//   newNodeId(2LE) newNodeEui64(8) status(1) policyDecision(1) parentOfNewNode(2LE)
		if len(frame.Parameters) < 14 {
			if c.settings.LogErrors {
				c.log.Warn("TRUST_CENTER_JOIN_HANDLER too short", slog.Int("bytes", len(frame.Parameters)))
			}
			return
		}
		p := frame.Parameters
		networkAddr := binary.LittleEndian.Uint16(p[0:2])
		ieeeAddr := binary.LittleEndian.Uint64(p[2:10])
		event := types.DeviceJoinEvent{
			NetworkAddress: networkAddr,
			IEEEAddress:    ieeeAddr,
		}
		if c.settings.LogCommands {
			c.log.Debug("device joined",
				slog.String("nwk", fmt.Sprintf("0x%04x", networkAddr)),
				slog.String("ieee", fmt.Sprintf("0x%016x", ieeeAddr)),
			)
		}
		select {
		case c.joinEvents <- event:
		default:
			// channel full, skip
		}

	case EZSP_STACK_STATUS_HANDLER:
		// STACK_STATUS_HANDLER callback: [status:uint8]
		// Status values: 0x90 = NETWORK_UP (SLStatus), 0x91 = NETWORK_DOWN, 0x15 = NETWORK_UP (EmberStatus)
		if len(frame.Parameters) >= 1 {
			status := frame.Parameters[0]
			if c.settings.LogCommands {
				c.log.Debug("STACK_STATUS callback", slog.String("status", fmt.Sprintf("0x%02X", status)))
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

// DeviceJoinEvents returns a channel that receives events when devices join the network.
func (c *Controller) DeviceJoinEvents() <-chan types.DeviceJoinEvent {
	return c.joinEvents
}

// registerZDOWaiter registers a waiter for a ZDO response cluster.
// The returned channel will receive the raw ZDP payload (after the APS header is stripped by INCOMING_MESSAGE_HANDLER).
// The caller must call removeZDOWaiter when done.
func (c *Controller) registerZDOWaiter(responseClusterID uint16) chan []byte {
	ch := make(chan []byte, 1)
	c.zdoWaitersMux.Lock()
	c.zdoWaiters[responseClusterID] = ch
	c.zdoWaitersMux.Unlock()
	return ch
}

// removeZDOWaiter removes a previously registered ZDO waiter.
func (c *Controller) removeZDOWaiter(responseClusterID uint16) {
	c.zdoWaitersMux.Lock()
	delete(c.zdoWaiters, responseClusterID)
	c.zdoWaitersMux.Unlock()
}

// Send sends a message to a device on the Zigbee network using EZSP_SEND_UNICAST.
func (c *Controller) Send(ctx context.Context, msg types.OutgoingMessage) error {
	// EZSP_SEND_UNICAST parameters (AN706):
	//   type(1) indexOrDestination(2LE) apsFrame(11) messageTag(1) messageLength(1) message[...]
	// EmberApsFrame: profileId(2LE) clusterId(2LE) srcEndpoint(1) dstEndpoint(1) options(2LE) groupId(2LE) sequence(1)
	params := make([]byte, 0, 16+len(msg.Data))
	params = append(params, 0x00) // type = EMBER_OUTGOING_DIRECT
	params = append(params, byte(msg.Destination.Short), byte(msg.Destination.Short>>8))
	// apsFrame
	params = append(params, 0x04, 0x01) // profileId = 0x0104 (Home Automation), little-endian
	params = append(params, byte(msg.ClusterID), byte(msg.ClusterID>>8))
	params = append(params, msg.SourceEndpoint)
	params = append(params, msg.DestinationEndpoint)
	params = append(params, 0x00, 0x00) // options = 0
	params = append(params, 0x00, 0x00) // groupId = 0
	params = append(params, 0x00)       // sequence (NCP assigns)
	params = append(params, 0x00)       // messageTag
	params = append(params, uint8(len(msg.Data)))
	params = append(params, msg.Data...)

	resp, err := c.sendEZSPCommand(EZSP_SEND_UNICAST, params, 5*time.Second)
	if err != nil {
		return fmt.Errorf("SEND_UNICAST: %w", err)
	}
	if len(resp.Parameters) < 1 {
		return fmt.Errorf("SEND_UNICAST response too short")
	}
	if resp.Parameters[0] != 0x00 {
		return fmt.Errorf("SEND_UNICAST status: 0x%02X", resp.Parameters[0])
	}
	return nil
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
		duration = 0xFE

		// Import "ZigBeeAlliance09" as a transient link key for the blank EUI64.
		// This allows devices with the pre-configured Zigbee interoperability key to join.
		// Must be called before PERMIT_JOINING; zigbee-herdsman does this in preJoining().
		zigbeeAllianceKey := []byte{
			0x5a, 0x69, 0x67, 0x42, 0x65, 0x65, 0x41, 0x6c,
			0x6c, 0x69, 0x61, 0x6e, 0x63, 0x65, 0x30, 0x39,
		}
		blankEUI64 := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
		keyParams := make([]byte, 0, 8+16+1)
		keyParams = append(keyParams, blankEUI64...)
		keyParams = append(keyParams, zigbeeAllianceKey...)
		keyParams = append(keyParams, 0x00) // SecManFlag.NONE (required for EZSP v < 0x0e)
		if impResp, impErr := c.sendEZSPCommand(EZSP_IMPORT_TRANSIENT_KEY, keyParams, 5*time.Second); impErr != nil {
			c.log.Warn("importTransientKey failed (continuing)", slog.Any("error", impErr))
		} else if len(impResp.Parameters) >= 4 {
			// Response is SLStatus uint32 LE
			slStatus := uint32(impResp.Parameters[0]) | uint32(impResp.Parameters[1])<<8 |
				uint32(impResp.Parameters[2])<<16 | uint32(impResp.Parameters[3])<<24
			if slStatus != 0 {
				c.log.Warn("importTransientKey non-OK status",
					slog.String("status", fmt.Sprintf("0x%08X", slStatus)))
			} else {
				c.log.Debug("transient key imported for ZigBeeAlliance09")
			}
		}

		// Set trust center policy to allow joins using preconfigured key.
		// Decision = ALLOW_JOINS(0x01) | ALLOW_UNSECURED_REJOINS(0x02) = 0x03
		// Matches zigbee-herdsman's USE_PRECONFIGURED_KEY decision.
		// SET_POLICY params: [policyId(1)][decisionId(1)] — 2 bytes total.
		tcDecision := EZSP_TC_DECISION_ALLOW_JOINS | EZSP_TC_DECISION_ALLOW_UNSECURED_REJOINS
		policyParams := []byte{byte(EZSP_POLICY_TRUST_CENTER), tcDecision}
		setResp, err := c.sendEZSPCommand(EZSP_SET_POLICY, policyParams, 5*time.Second)
		if err != nil {
			c.log.Warn("setPolicy(TRUST_CENTER) failed", slog.Any("error", err))
		} else if len(setResp.Parameters) >= 1 && setResp.Parameters[0] != 0x00 {
			c.log.Warn("setPolicy(TRUST_CENTER) non-OK status",
				slog.String("status", fmt.Sprintf("0x%02X", setResp.Parameters[0])),
				slog.String("full_response", fmt.Sprintf("%X", setResp.Parameters)))
		} else {
			c.log.Debug("trust center policy set", slog.String("decision", fmt.Sprintf("0x%02X", tcDecision)))
		}

		// Read back the current TC policy so we can see what the NCP actually has.
		getResp, err := c.sendEZSPCommand(EZSP_GET_POLICY, []byte{byte(EZSP_POLICY_TRUST_CENTER)}, 5*time.Second)
		if err != nil {
			c.log.Warn("getPolicy(TRUST_CENTER) failed", slog.Any("error", err))
		} else if len(getResp.Parameters) >= 2 {
			// GET_POLICY response: [EzspStatus(1)][EzspDecisionId(1)]
			status := getResp.Parameters[0]
			decision := getResp.Parameters[1]
			c.log.Info("current TRUST_CENTER policy from NCP",
				slog.String("status", fmt.Sprintf("0x%02X", status)),
				slog.String("decision", fmt.Sprintf("0x%02X", decision)),
				slog.Bool("allow_joins", decision&EZSP_TC_DECISION_ALLOW_JOINS != 0),
				slog.Bool("allow_unsecured_rejoins", decision&EZSP_TC_DECISION_ALLOW_UNSECURED_REJOINS != 0),
				slog.Bool("send_key_in_clear", decision&EZSP_TC_DECISION_SEND_KEY_IN_CLEAR != 0),
				slog.Bool("defer_joins", decision&EZSP_TC_DECISION_DEFER_JOINS != 0),
			)
		}
	} else {
		// Closing permit join: remove transient keys and revert to rejoins-only policy.
		if _, clrErr := c.sendEZSPCommand(EZSP_CLEAR_TRANSIENT_LINK_KEYS, nil, 5*time.Second); clrErr != nil {
			c.log.Warn("clearTransientLinkKeys failed (continuing)", slog.Any("error", clrErr))
		}
		rejoinsOnly := []byte{byte(EZSP_POLICY_TRUST_CENTER), EZSP_TC_DECISION_ALLOW_UNSECURED_REJOINS}
		if _, err := c.sendEZSPCommand(EZSP_SET_POLICY, rejoinsOnly, 5*time.Second); err != nil {
			c.log.Warn("setPolicy(TRUST_CENTER, ALLOW_REJOINS_ONLY) failed (continuing)", slog.Any("error", err))
		}
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
	if status != 0x00 {
		// 0x22 is returned by EZSP v13 firmware even on success (NCP sends
		// STACK_STATUS NETWORK_OPENED callback to confirm); treat as non-fatal.
		if status == 0x22 {
			c.log.Debug("permit joining returned 0x22 (non-fatal, NETWORK_OPENED callback expected)")
		} else {
			return fmt.Errorf("permit joining command failed with status 0x%02X", status)
		}
	}

	if enabled {
		c.log.Info("permit joining enabled", slog.Int("duration_sec", int(duration)))
		// Log network info so operator can verify the device is joining the right network.
		if info, err := c.GetNetworkInfo(ctx); err == nil && info.State == types.NetworkStateUp {
			c.log.Info("coordinator network info",
				slog.Int("channel", int(info.Channel)),
				slog.String("pan_id", fmt.Sprintf("0x%04X", info.PanID)),
				slog.String("extended_pan_id", fmt.Sprintf("0x%016X", info.ExtendedPanID)),
			)
		}
	} else if c.settings.LogCommands {
		c.log.Debug("permit joining disabled")
	}

	return nil
}

// sendEZSPCommand sends an EZSP command and waits for a response.
// Returns the EZSP frame response.
func (c *Controller) sendEZSPCommand(frameID EZSPFrameID, params []byte, timeout time.Duration) (*EZSPFrame, error) {
	// Get next sequence number
	seqNum := c.port.NextSequence()

	ezspFrame := &EZSPFrame{
		Sequence:   seqNum,
		Control:    EZSP_FRAME_CONTROL_COMMAND,
		FrameID:    frameID,
		Parameters: params,
	}

	// Register handler BEFORE sending
	handler := c.registerHandler(seqNum, timeout)

	// Use extended (5-byte) EZSP header format for all commands after VERSION negotiation.
	// The NCP detects extended format by frameControlHi (byte[2]) & 0x03 == 0x01.
	// Some frame IDs like 0x55 (SET_POLICY) have bits[0:1]==0x01 and would be accidentally
	// parsed as extended by the NCP when sent in legacy format, causing them to fail.
	ezspData := SerializeEZSPFrameExtended(ezspFrame)
	control := byte((c.txSeq << 4) | (c.rxSeq & 0x07))
	ashDataFrame := buildASHDataFrame(control, ezspData)
	c.txSeq = (c.txSeq + 1) % 8

	// Store control+ezspData for retransmit on NAK (reTx=1 rebuild).
	c.lastDataMux.Lock()
	c.lastFrameControl = control
	c.lastFrameEZSP = ezspData
	c.lastDataMux.Unlock()

	// Without hardware flow control (CP2102N has no RTS/CTS), the NCP can't
	// signal backpressure. Wait before each frame to let the NCP process the
	// previous one and clear its UART buffer.
	if c.settings.DisableFlowControl {
		time.Sleep(200 * time.Millisecond)
	}

	c.log.Debug("sending EZSP command", slog.String("frame_id", fmt.Sprintf("0x%04X", frameID)), slog.Int("sequence", int(seqNum)), slog.String("frame", fmt.Sprintf("%X", ashDataFrame)))

	if _, err := c.port.WriteBytes(ashDataFrame); err != nil {
		c.removeHandler(seqNum, handler)
		return nil, fmt.Errorf("sending EZSP command 0x%04X: %w", frameID, err)
	}

	// Wait for response
	response, err := handler.Receive()
	if err != nil {
		// Command timed out — the ASH session is likely stuck (e.g. after NAK retransmit
		// was never ACK'd). Reset ASH and retry once.
		c.resetASHSession()
		c.log.Warn("EZSP command timed out, retrying after ASH reset",
			slog.String("frame_id", fmt.Sprintf("0x%04X", frameID)))

		// Retry: re-register handler, rebuild frame with new sequence, and send again
		retrySeqNum := c.port.NextSequence()
		retryFrame := &EZSPFrame{
			Sequence:   retrySeqNum,
			Control:    EZSP_FRAME_CONTROL_COMMAND,
			FrameID:    frameID,
			Parameters: params,
		}
		retryHandler := c.registerHandler(retrySeqNum, timeout)
		retryEzspData := SerializeEZSPFrameExtended(retryFrame)
		retryControl := byte((c.txSeq << 4) | (c.rxSeq & 0x07))
		retryASHFrame := buildASHDataFrame(retryControl, retryEzspData)
		c.txSeq = (c.txSeq + 1) % 8

		c.lastDataMux.Lock()
		c.lastFrameControl = retryControl
		c.lastFrameEZSP = retryEzspData
		c.lastDataMux.Unlock()

		if c.settings.DisableFlowControl {
			time.Sleep(200 * time.Millisecond)
		}
		if _, writeErr := c.port.WriteBytes(retryASHFrame); writeErr != nil {
			c.removeHandler(retrySeqNum, retryHandler)
			return nil, fmt.Errorf("retry sending EZSP command 0x%04X: %w", frameID, writeErr)
		}
		response, retryErr := retryHandler.Receive()
		if retryErr != nil {
			c.resetASHSession()
			return nil, fmt.Errorf("retry waiting for EZSP command 0x%04X: %w", frameID, retryErr)
		}
		return response, nil
	}

	return response, nil
}

// SetConcentrator configures the NCP as a high-RAM source route concentrator.
// This enables Many-to-One Route Request (MTORR) broadcasts so devices can
// discover routes back to the coordinator. Must be called after network is up.
func (c *Controller) SetConcentrator(ctx context.Context) error {
	// SET_CONCENTRATOR params: enabled(1), type(2LE), minTime(2LE), maxTime(2LE),
	//   routeErrorThreshold(1), deliveryFailureThreshold(1), maxHops(1)
	params := []byte{
		0x01,       // enabled = true
		0xFE, 0xFF, // HIGH_RAM_CONCENTRATOR = 0xFFFE (little-endian)
		0x05, 0x00, // minTime = 5 seconds
		0x3C, 0x00, // maxTime = 60 seconds
		0x03, // routeErrorThreshold = 3
		0x01, // deliveryFailureThreshold = 1
		0x00, // maxHops = 0 (use default)
	}
	resp, err := c.sendEZSPCommand(EZSP_SET_CONCENTRATOR, params, 5*time.Second)
	if err != nil {
		return fmt.Errorf("setConcentrator: %w", err)
	}
	if len(resp.Parameters) >= 1 && resp.Parameters[0] != 0x00 {
		c.log.Warn("setConcentrator non-OK status", slog.String("status", fmt.Sprintf("0x%02X", resp.Parameters[0])))
	} else {
		c.log.Info("concentrator mode enabled (HIGH_RAM)")
	}

	// Enable source route discovery
	if _, err := c.sendEZSPCommand(EZSP_SET_SOURCE_ROUTE_DISC, []byte{0x01}, 5*time.Second); err != nil {
		c.log.Warn("setSourceRouteDiscoveryMode failed (continuing)", slog.Any("error", err))
	} else {
		c.log.Debug("source route discovery mode set to RESCHEDULE")
	}

	return nil
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

	// Map EmberNetworkStatus enum to NetworkState.
	// EZSP_NETWORK_STATE returns EmberNetworkStatus (NOT SLStatus):
	//   0x00 = NO_NETWORK, 0x01 = JOINING, 0x02 = JOINED, 0x03 = JOINED_NO_PARENT
	var networkState types.NetworkState
	switch networkStatus {
	case 0x02, 0x03: // JOINED_NETWORK, JOINED_NETWORK_NO_PARENT
		networkState = types.NetworkStateUp
	case 0x00: // NO_NETWORK
		networkState = types.NetworkStateNotJoined
	case 0x01: // JOINING_NETWORK
		networkState = types.NetworkStateDown
	default:
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

	// Extended PAN ID (8 bytes, big-endian per Zigbee/Ember convention)
	extendedPanIDBytes := networkParamsResp.Parameters[offset : offset+8]
	offset += 8
	extendedPanID := binary.BigEndian.Uint64(extendedPanIDBytes)

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

// initEzsp configures the NCP after the VERSION handshake and before any
// network operations. Without this, all network commands return 0x58
// (TRANSMIT_BLOCKED / SL_STATUS_TRANSMIT_BLOCKED) because the radio stack
// is not yet initialized.
//
// Mirrors zigbee-herdsman's initEzsp() → registerFixedEndpoints() →
// initTrustCenter() sequence (EmberZNet 7.x / EZSP protocol 13).
func (c *Controller) initEzsp() error {
	c.log.Debug("initializing EZSP NCP configuration")

	// 1. Set mandatory stack configuration values.
	// STACK_PROFILE=2 (ZigBee Pro) and SECURITY_LEVEL=5 (AES-128) are the
	// minimum required to enable the radio stack; omitting them causes all
	// network operations to return 0x58.
	type configEntry struct {
		id    EZSPConfigId
		value uint16
		name  string
	}
	configs := []configEntry{
		{EZSP_CONFIG_STACK_PROFILE, 2, "stack_profile"},
		// NOTE: SECURITY_LEVEL (0x0D) consistently NAKs on this hardware (CP2102/EFR32MG21).
		// The NCP already has security level 5 in NVM from prior formation, so skip it.
		// {EZSP_CONFIG_SECURITY_LEVEL, 5, "security_level"},
		{EZSP_CONFIG_SUPPORTED_NETWORKS, 1, "supported_networks"},
		{EZSP_CONFIG_TRUST_CENTER_ADDRESS_CACHE_SIZE, 2, "tc_address_cache_size"},
		{EZSP_CONFIG_INDIRECT_TRANSMISSION_TIMEOUT, 7680, "indirect_transmission_timeout"},
		{EZSP_CONFIG_MAX_HOPS, 30, "max_hops"},
		{EZSP_CONFIG_MAX_END_DEVICE_CHILDREN, 32, "max_end_device_children"},
		{EZSP_CONFIG_END_DEVICE_POLL_TIMEOUT, 8, "end_device_poll_timeout"}, // 2^8 = 256 seconds
		{EZSP_CONFIG_TRANSIENT_KEY_TIMEOUT_S, 300, "transient_key_timeout_s"},
	}
	for _, cfg := range configs {
		params := []byte{byte(cfg.id), byte(cfg.value), byte(cfg.value >> 8)}
		const maxAttempts = 3
		var resp *EZSPFrame
		var err error
		for attempt := 0; attempt < maxAttempts; attempt++ {
			if attempt > 0 {
				c.log.Warn("retrying setConfigurationValue",
					slog.String("config", cfg.name),
					slog.Int("attempt", attempt+1),
				)
				time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
			}
			resp, err = c.sendEZSPCommand(EZSP_SET_CONFIGURATION_VALUE, params, 10*time.Second)
			if err == nil {
				break
			}
			c.log.Warn("setConfigurationValue failed, will retry",
				slog.String("config", cfg.name),
				slog.String("error", err.Error()),
			)
		}
		if err != nil {
			return fmt.Errorf("setConfigurationValue(%s): %w", cfg.name, err)
		}
		if len(resp.Parameters) >= 1 && resp.Parameters[0] != 0x00 {
			// 0x46 (INVALID_CALL) can mean the value is already set (read-only after boot)
			// or the config ID is unsupported — treat as non-fatal.
			c.log.Warn("setConfigurationValue non-OK status (continuing)",
				slog.String("config", cfg.name),
				slog.String("status", fmt.Sprintf("0x%02X", resp.Parameters[0])),
			)
		}
		// Small inter-command delay: the NCP may need a moment to process
		// each configuration change before accepting the next command.
		time.Sleep(50 * time.Millisecond)
	}

	// NOTE: SECURITY_LEVEL (0x0D) cannot be queried or set on this hardware (CP2102/EFR32MG21) —
	// both GET and SET NAK and leave the ASH session stuck, breaking all subsequent commands.
	// The NCP retains security level 5 (AES-128) in NVM from prior formation; trust it.

	// 2. Register application endpoint so the NCP can participate in the network.
	if err := c.addEndpoint(1); err != nil {
		// Non-fatal: endpoint may already be registered from a previous Start().
		c.log.Warn("addEndpoint failed (continuing)", slog.Any("error", err))
	}

	// 3. Set trust center policies for a coordinator that allows devices to join.
	type policyEntry struct {
		id       EZSPPolicyId
		decision uint8
		name     string
	}
	policies := []policyEntry{
		{
			EZSP_POLICY_TRUST_CENTER,
			EZSP_TC_DECISION_ALLOW_JOINS | EZSP_TC_DECISION_ALLOW_UNSECURED_REJOINS,
			"trust_center",
		},
		{
			EZSP_POLICY_TC_KEY_REQUEST,
			0x51, // ALLOW_TC_KEY_REQUESTS_AND_SEND_CURRENT_KEY
			"tc_key_request",
		},
		{EZSP_POLICY_APP_KEY_REQUEST, 0x00 /* DENY */, "app_key_request"},
		{EZSP_POLICY_MSG_CONTENTS_CB, 0x01 /* MESSAGE_TAG_ONLY */, "msg_contents_cb"},
	}
	for _, pol := range policies {
		// SET_POLICY params: [policyId(1)][decisionId(1)] — exactly 2 bytes.
		params := []byte{byte(pol.id), pol.decision}
		resp, err := c.sendEZSPCommand(EZSP_SET_POLICY, params, 10*time.Second)
		if err != nil {
			return fmt.Errorf("setPolicy(%s): %w", pol.name, err)
		}
		if len(resp.Parameters) >= 1 && resp.Parameters[0] != 0x00 {
			c.log.Warn("setPolicy non-OK status (continuing)",
				slog.String("policy", pol.name),
				slog.String("status", fmt.Sprintf("0x%02X", resp.Parameters[0])),
				slog.String("response", fmt.Sprintf("%X", resp.Parameters)),
			)
		}
	}

	c.log.Debug("EZSP NCP configuration complete")
	return nil
}

// addEndpoint registers an application endpoint on the NCP.
// Endpoint 1, Home Automation profile (0x0104), coordinator device, Basic cluster.
// Returns nil if the endpoint is already registered (0x46 INVALID_CALL).
func (c *Controller) addEndpoint(endpoint uint8) error {
	// ADD_ENDPOINT parameters (AN706):
	//   endpoint(1) profileId(2LE) deviceId(2LE) deviceVersion(1)
	//   inputClusterCount(1) [inputCluster(2LE)...]
	//   outputClusterCount(1) [outputCluster(2LE)...]
	params := []byte{
		endpoint,
		0x04, 0x01, // profileId = 0x0104 (Home Automation), little-endian
		0x00, 0x00, // deviceId = 0x0000
		0x00,       // deviceVersion
		0x01,       // inputClusterCount = 1
		0x00, 0x00, // Basic cluster (0x0000)
		0x00, // outputClusterCount = 0
	}
	resp, err := c.sendEZSPCommand(EZSP_ADD_ENDPOINT, params, 10*time.Second)
	if err != nil {
		return fmt.Errorf("addEndpoint: %w", err)
	}
	if len(resp.Parameters) < 1 {
		return fmt.Errorf("addEndpoint response too short")
	}
	if resp.Parameters[0] == 0x46 { // INVALID_CALL — already registered
		c.log.Debug("addEndpoint: endpoint already registered")
		return nil
	}
	if resp.Parameters[0] != 0x00 {
		return fmt.Errorf("addEndpoint status: 0x%02X", resp.Parameters[0])
	}
	return nil
}

// setInitialSecurityState configures the NCP's trust center security so it can
// distribute the network key to joining devices. This must be called both when
// forming a new network AND when restoring from NVM — the NCP does not persist
// trust center runtime state across restarts.
func (c *Controller) setInitialSecurityState(params types.NetworkParameters) error {
	// Well-known Zigbee 3.0 Trust Center Link Key ("ZigBeeAlliance09")
	wellKnownLinkKey := []byte{
		0x5A, 0x69, 0x67, 0x42, 0x65, 0x65, 0x41, 0x6C,
		0x6C, 0x69, 0x61, 0x6E, 0x63, 0x65, 0x30, 0x39,
	}

	// Format: [bitmask:uint16][preconfiguredKey:16][networkKey:16][networkKeySequenceNumber:uint8][preconfiguredTrustCenterEui64:8]
	securityParams := make([]byte, 0, 2+16+16+1+8)

	// TRUST_CENTER_GLOBAL_LINK_KEY (0x0004) | HAVE_PRECONFIGURED_KEY (0x0100) | HAVE_NETWORK_KEY (0x0200)
	// | TRUST_CENTER_USES_HASHED_LINK_KEY (0x0084) | REQUIRE_ENCRYPTED_KEY (0x0800)
	bitmask := uint16(0x0004 | 0x0100 | 0x0200 | 0x0084 | 0x0800)
	securityParams = append(securityParams, byte(bitmask), byte(bitmask>>8))
	securityParams = append(securityParams, wellKnownLinkKey...)
	securityParams = append(securityParams, params.NetworkKey[:]...)
	securityParams = append(securityParams, 0x00)               // network key sequence number
	securityParams = append(securityParams, make([]byte, 8)...) // trust center EUI64 (zeros)

	c.log.Debug("setting initial security state")
	securityResp, err := c.sendEZSPCommand(EZSP_SET_INITIAL_SECURITY_STATE, securityParams, 5*time.Second)
	if err != nil {
		return fmt.Errorf("setting initial security state: %w", err)
	}
	if len(securityResp.Parameters) < 1 {
		return fmt.Errorf("security state response too short")
	}
	securityStatus := securityResp.Parameters[0]
	if securityStatus == 0x00 || securityStatus == 0x46 {
		c.log.Debug("initial security state set successfully")
	} else if securityStatus == 0x58 || securityStatus == 0x68 {
		c.log.Warn("SET_INITIAL_SECURITY_STATE non-fatal status (continuing)",
			slog.String("status", fmt.Sprintf("0x%02X", securityStatus)))
	} else {
		return fmt.Errorf("setting initial security state failed with status 0x%02X", securityStatus)
	}

	// Extended security bitmask: JOINER_GLOBAL_LINK_KEY | NWK_LEAVE_REQUEST_NOT_ALLOWED
	c.log.Debug("setting extended security bitmask")
	extendedBitmask := uint16(0x0001 | 0x0002)
	extendedSecurityResp, err := c.sendEZSPCommand(EZSP_SET_EXTENDED_SECURITY_BITMASK,
		[]byte{byte(extendedBitmask), byte(extendedBitmask >> 8)}, 5*time.Second)
	if err != nil {
		c.log.Warn("SET_EXTENDED_SECURITY_BITMASK failed (continuing)", slog.Any("error", err))
	} else if len(extendedSecurityResp.Parameters) >= 1 {
		extendedStatus := extendedSecurityResp.Parameters[0]
		if extendedStatus == 0x00 {
			c.log.Debug("extended security bitmask set successfully")
		} else {
			c.log.Warn("SET_EXTENDED_SECURITY_BITMASK non-OK status",
				slog.String("status", fmt.Sprintf("0x%02X", extendedStatus)))
		}
	}

	return nil
}

// FormNetwork creates a new Zigbee network with the specified parameters.
// This must be called before Start() if the device is not already part of a network.
// The network parameters should be persisted so the same network can be restored
// when swapping devices.
func (c *Controller) FormNetwork(ctx context.Context, params types.NetworkParameters) error {
	c.log.Info("forming network", slog.Uint64("pan_id", uint64(params.PanID)), slog.Uint64("extended_pan_id", params.ExtendedPanID), slog.Int("channel", int(params.Channel)))

	// Step -1 (optional): Force LEAVE_NETWORK to try to get device into a clean state.
	// zigbee-herdsman does NOT send LEAVE before NETWORK_INIT. If the device returns 0x58,
	// try -zigbeecoordinator.ember-skip-forced-leave to match herdsman's order.
	if !c.settings.SkipForcedLeave {
		c.log.Debug("step -1: forcing LEAVE_NETWORK for clean state")
		leaveResp, leaveErr := c.sendEZSPCommand(EZSP_LEAVE_NETWORK, nil, 5*time.Second)
		if leaveErr == nil && len(leaveResp.Parameters) >= 1 {
			leaveStatus := leaveResp.Parameters[0]
			c.log.Debug("LEAVE_NETWORK status", slog.String("status", fmt.Sprintf("0x%02X", leaveStatus)))
		} else {
			c.log.Warn("LEAVE_NETWORK failed or timed out (continuing)", slog.Any("error", leaveErr))
		}
		time.Sleep(1 * time.Second)
	} else {
		c.log.Debug("step -1: skipped (ember-skip-forced-leave; matching zigbee-herdsman order)")
	}

	// Call NETWORK_INIT to restore network from tokens/NV memory.
	// Security state must NOT be set before this — it would overwrite NVM tokens
	// and prevent the restore. SET_INITIAL_SECURITY_STATE is only needed when forming new.
	// - SUCCESS (0x00): network restored from stored state
	// - EMBER_NOT_JOINED (0x93) / SL_STATUS_NOT_JOINED (0x17): no network, need to form
	c.log.Debug("step 0: calling NETWORK_INIT to restore from tokens")

	// EmberNetworkInitStruct: bitmask (uint16, little-endian)
	// zigbee-herdsman uses: PARENT_INFO_IN_TOKEN (0x01) | END_DEVICE_REJOIN_ON_REBOOT (0x02) = 0x03
	networkInitParams := []byte{0x03, 0x00} // Little-endian: 0x0003

	networkInitResp, err := c.sendEZSPCommand(EZSP_NETWORK_INIT, networkInitParams, 5*time.Second)
	if err != nil {
		c.log.Warn("NETWORK_INIT failed (will attempt to form)", slog.Any("error", err))
		// Continue to form network
	} else if len(networkInitResp.Parameters) >= 1 {
		networkInitStatus := networkInitResp.Parameters[0]
		c.log.Debug("NETWORK_INIT status", slog.String("status", fmt.Sprintf("0x%02X", networkInitStatus)))

		// Status 0x00 = SUCCESS: Network was restored from tokens
		// Status 0x17 = SL_STATUS_NOT_JOINED (EmberZNet 7.x SL_STATUS encoding) — no network exists
		// Status 0x93 = EMBER_NOT_JOINED (old EmberStatus encoding) — no network exists
		// Status 0x58 = TRANSMIT_BLOCKED: Unknown state (device may be in invalid state)
		if networkInitStatus == 0x00 {
			// Network was restored! Wait for STACK_STATUS_NETWORK_UP callback
			// Then verify network parameters match what we want
			c.log.Debug("network restored from tokens, waiting for NETWORK_UP callback")

			// Wait up to 5 seconds for network to come up
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				// Poll NETWORK_STATE to check if network is up
				stateResp, err := c.sendEZSPCommand(EZSP_NETWORK_STATE, nil, 2*time.Second)
				if err == nil && len(stateResp.Parameters) >= 1 {
					state := stateResp.Parameters[0]
					if state == 2 { // JOINED_NETWORK
						// Network is up! Verify parameters match
						c.log.Debug("network is up, verifying parameters match")

						// Get network parameters and compare
						currentInfo, err := c.GetNetworkInfo(ctx)
						if err == nil && currentInfo.State == types.NetworkStateUp {
							panIDMatch := currentInfo.PanID == params.PanID
							extendedPanIDMatch := currentInfo.ExtendedPanID == params.ExtendedPanID
							channelMatch := uint8(currentInfo.Channel) == params.Channel

							if panIDMatch && extendedPanIDMatch && channelMatch {
								c.log.Debug("restored network matches desired parameters, no need to form")
								return nil
							}
							// Network exists but parameters don't match - leave and form new
							c.log.Info("restored network parameters don't match, leaving network",
								slog.Uint64("current_pan_id", uint64(currentInfo.PanID)), slog.Uint64("current_ext_pan_id", currentInfo.ExtendedPanID), slog.Int("current_channel", int(currentInfo.Channel)),
								slog.Uint64("desired_pan_id", uint64(params.PanID)), slog.Uint64("desired_ext_pan_id", params.ExtendedPanID), slog.Int("desired_channel", int(params.Channel)))
							leaveResp, _ := c.sendEZSPCommand(EZSP_LEAVE_NETWORK, nil, 5*time.Second)
							if len(leaveResp.Parameters) >= 1 {
								c.log.Debug("LEAVE_NETWORK status", slog.String("status", fmt.Sprintf("0x%02X", leaveResp.Parameters[0])))
							}
							time.Sleep(1 * time.Second) // Wait for network to go down
						} else {
							c.log.Debug("network is up but couldn't verify parameters, assuming correct")
							return nil
						}
						break
					}
				}
				time.Sleep(500 * time.Millisecond)
			}

			// If we get here, network didn't come up in time or parameters didn't match
			// Continue to form network below
		} else if networkInitStatus == 0x93 || networkInitStatus == 0x17 {
			// EMBER_NOT_JOINED (0x93 old EmberStatus) or SL_STATUS_NOT_JOINED (0x17 EmberZNet 7.x)
			// Sync response says no network, but NCP may send STACK_STATUS NETWORK_UP
			// asynchronously right after (indicating a network was restored from NVM tokens).
			// Drain any buffered callback before deciding to form.
			c.log.Debug("no network found (NOT_JOINED), checking for async NETWORK_UP callback",
				slog.String("status", fmt.Sprintf("0x%02X", networkInitStatus)))
			select {
			case cbStatus := <-c.stackStatusCh:
				if cbStatus == 0x90 || cbStatus == 0x15 { // NETWORK_UP
					c.log.Info("STACK_STATUS NETWORK_UP received after NOT_JOINED — network already up (NVM restored)",
						slog.String("status", fmt.Sprintf("0x%02X", cbStatus)))
					return nil
				}
				c.log.Debug("STACK_STATUS callback (not NETWORK_UP), proceeding to form",
					slog.String("status", fmt.Sprintf("0x%02X", cbStatus)))
			case <-time.After(200 * time.Millisecond):
				c.log.Debug("no async NETWORK_UP callback, proceeding to form")
			}
			// Continue to form network below
		} else {
			// Unknown status (likely 0x58) - log but continue
			if c.settings.LogErrors {
				c.log.Warn("NETWORK_INIT returned unexpected status, will attempt to form anyway",
					slog.String("status", fmt.Sprintf("0x%02X", networkInitStatus)),
					slog.Int("response_len", len(networkInitResp.Parameters)),
					slog.String("response_hex", fmt.Sprintf("%X", networkInitResp.Parameters)))
			}
			// Continue to form network below
		}
	}

	// Set security state and clear key table before forming a new network.
	// This is only reached when NETWORK_INIT did NOT restore — either no network exists
	// or the parameters didn't match.
	if err := c.setInitialSecurityState(params); err != nil {
		return err
	}

	c.log.Debug("clearing key table before forming network")
	clearKeyResp, clearKeyErr := c.sendEZSPCommand(EZSP_CLEAR_KEY_TABLE, nil, 5*time.Second)
	if clearKeyErr == nil && len(clearKeyResp.Parameters) >= 1 {
		clearKeyStatus := clearKeyResp.Parameters[0]
		if clearKeyStatus != 0x00 {
			c.log.Warn("CLEAR_KEY_TABLE non-OK status", slog.String("status", fmt.Sprintf("0x%02X", clearKeyStatus)))
		} else {
			c.log.Debug("key table cleared successfully")
		}
	} else if clearKeyErr != nil {
		c.log.Warn("CLEAR_KEY_TABLE failed (continuing)", slog.Any("error", clearKeyErr))
	}

	// Small delay before forming network to let device settle
	time.Sleep(500 * time.Millisecond)

	// Step 2: Form network with network parameters
	// Format: [extendedPanId:8 bytes][panId:uint16][radioTxPower:int8][radioChannel:uint8][joinMethod:uint8][nwkManagerId:uint16][nwkUpdateId:uint8][channels:uint32]
	// Herdsman writes extendedPanId as writeListUInt8 (raw 8 bytes, MSB-first / big-endian convention).
	networkParams := make([]byte, 0, 8+2+1+1+1+2+1+4)

	// Extended PAN ID (8 bytes, big-endian to match Zigbee/Ember and herdsman byte order)
	extPanIDBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(extPanIDBytes, params.ExtendedPanID)
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

	c.log.Debug("sending FORM_NETWORK command")

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
		c.log.Warn("FORM_NETWORK timed out or failed (will poll NETWORK_STATE)", slog.Any("error", err))
		// Continue to polling below - don't return error yet
	} else if len(formResp.Parameters) >= 1 {
		formStatus = formResp.Parameters[0]
		// Status 0x00 = SUCCESS. zigbee-herdsman requires SLStatus.OK (0x00) for FORM_NETWORK and throws otherwise.
		// We align: treat 0x58 (TRANSMIT_BLOCKED) as rejection and fail fast instead of waiting 45s for NETWORK_UP
		// (see docs/reference/ember/formation-vs-herdsman.md).
		if formStatus == 0x00 {
			c.log.Debug("network formation initiated successfully")
		} else if formStatus == 0x58 {
			// 0x58 = TRANSMIT_BLOCKED: definitively rejected, no point waiting for callback
			return fmt.Errorf("form network rejected: device returned 0x58 (TRANSMIT_BLOCKED); try power cycle or different firmware/dongle")
		} else if formStatus >= 0x18 && formStatus <= 0x1F {
			// SL_STATUS scan/network-operation lifecycle range:
			// 0x18 = NO_BEACONS_HEARD, 0x19 = NO_NETWORKS_FOUND, 0x1B = SCAN_IN_PROGRESS
			// 0x1E = likely formation async accepted (energy scan in progress before channel selection)
			// Proceed to wait for STACK_STATUS_HANDLER NETWORK_UP callback.
			c.log.Warn("FORM_NETWORK returned in-progress status, waiting for NETWORK_UP",
				slog.String("status", fmt.Sprintf("0x%02X", formStatus)))
		} else {
			if c.settings.LogErrors {
				c.log.Warn("FORM_NETWORK rejected",
					slog.Int("response_len", len(formResp.Parameters)),
					slog.String("response_hex", fmt.Sprintf("%X", formResp.Parameters)))
			}
			return fmt.Errorf("form network command failed with status 0x%02X", formStatus)
		}
	} else {
		c.log.Warn("FORM_NETWORK response too short (treating as timeout, will poll)")
	}

	c.log.Debug("waiting for network to form (may take several seconds)")

	// Network formation is asynchronous - we need to wait for the network to come up
	// zigbee-herdsman waits for STACK_STATUS_NETWORK_UP callback, but also polls as fallback
	// Status values: 0x90 = NETWORK_UP (SLStatus), 0x15 = NETWORK_UP (EmberStatus), 0x02 = JOINED_NETWORK
	// NOTE: Network formation can take 10-60 seconds depending on device and firmware
	// zigbee-herdsman uses 10s timeout for Ember, but Z-Stack can take up to 60s
	// We use 45 seconds to be safe (zigbee-herdsman uses 10s; we poll as fallback so allow more)
	timeout := 45 * time.Second
	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second
	lastPoll := time.Now()
	startTime := time.Now()
	lastPolledState := byte(0xFF) // 0xFF = no successful poll yet; used in timeout error

	c.log.Debug("waiting for STACK_STATUS or NETWORK_STATE poll", slog.Duration("timeout", timeout))

	for time.Now().Before(deadline) {
		elapsed := time.Since(startTime)

		// First, check for STACK_STATUS callback (preferred method)
		select {
		case status := <-c.stackStatusCh:
			if status == 0x90 || status == 0x15 { // NETWORK_UP
				c.log.Info("network is up (from callback)", slog.String("status", fmt.Sprintf("0x%02X", status)), slog.Duration("elapsed", elapsed.Round(time.Second)))
				return nil
			} else if status == 0x91 { // NETWORK_DOWN
				return fmt.Errorf("network formation failed: device reported NETWORK_DOWN (0x91)")
			}
			c.log.Debug("STACK_STATUS callback (waiting for NETWORK_UP)", slog.String("status", fmt.Sprintf("0x%02X", status)))
		case <-time.After(500 * time.Millisecond):
			// No callback received, poll NETWORK_STATE as fallback
			if time.Since(lastPoll) >= pollInterval {
				lastPoll = time.Now()
				stateResp, err := c.sendEZSPCommand(EZSP_NETWORK_STATE, nil, 2*time.Second)
				if err == nil && len(stateResp.Parameters) >= 1 {
					state := stateResp.Parameters[0]
					lastPolledState = state
					if state == 0x02 || state == 0x03 { // JOINED_NETWORK or JOINED_NETWORK_NO_PARENT
						c.log.Info("network is up (from polling)", slog.String("status", fmt.Sprintf("0x%02X", state)), slog.Duration("elapsed", elapsed.Round(time.Second)))
						return nil
					}
					c.log.Debug("polled NETWORK_STATE", slog.String("state", fmt.Sprintf("0x%02X", state)), slog.Duration("elapsed", elapsed.Round(time.Second)), slog.Duration("remaining", time.Until(deadline).Round(time.Second)))
				} else if err != nil {
					c.log.Warn("error polling NETWORK_STATE (continuing)", slog.Any("error", err))
				}
			}
		}
	}

	// Timeout - network didn't form
	elapsed := time.Since(startTime)
	errMsg := fmt.Sprintf("timeout waiting for network to form after FORM_NETWORK (elapsed: %v, FORM_NETWORK returned 0x%02X)",
		elapsed.Round(time.Second), formStatus)
	if lastPolledState != 0xFF {
		errMsg += fmt.Sprintf("; last NETWORK_STATE poll: 0x%02X", lastPolledState)
	}
	return fmt.Errorf("%s", errMsg)
}

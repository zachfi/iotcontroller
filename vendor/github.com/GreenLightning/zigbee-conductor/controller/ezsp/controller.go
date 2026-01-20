// Package ezsp implements a Controller for Silicon Labs EZSP (EmberZNet Serial Protocol) devices.
//
// EZSP is also known as "EMBER" in some open-source implementations (e.g., Zigbee2MQTT).
// The protocol itself is the same - this is primarily a naming convention change.
// This implementation supports devices like:
//   - Sonoff Dongle Max (Dongle-M)
//   - Sonoff ZBDongle-E
//   - Other Silicon Labs EZSP-based coordinators
//
// The protocol uses ASH (Asynchronous Serial Host) framing over a serial connection.
// Documentation: UG100 EZSP Reference Guide, AN706 EZSP UART Host Interfacing Guide
package ezsp

import (
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/GreenLightning/zigbee-conductor/zigbee"
)

var _ zigbee.Controller = (*Controller)(nil)

var (
	ErrTimeout = errors.New("command timed out")
)

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

type Controller struct {
	settings   zigbee.ControllerSettings
	port       *Port
	handlers   map[uint8]*Handler // Map sequence number to handler
	handlerMux sync.Mutex
	readLoopDone chan struct{}
}

func NewController(settings zigbee.ControllerSettings) (*Controller, error) {
	port, err := NewPort(settings.Port)
	if err != nil {
		return nil, err
	}
	c := &Controller{
		settings:     settings,
		port:         port,
		handlers:     make(map[uint8]*Handler),
		readLoopDone: make(chan struct{}),
	}
	
	log.Printf("[ezsp] Serial port opened: %s\n", settings.Port)
	
	// IMPORTANT: Sonoff Dongle Max has a wake-up button on top
	// The device may be in sleep mode and needs the button pressed to wake up
	// OR the device needs to be woken via serial communication
	log.Printf("[ezsp] NOTE: If device doesn't respond, try pressing the button on top of the device\n")
	log.Printf("[ezsp] The device may be in sleep mode and needs physical wake-up\n")
	
	// Test if we can write to the serial port
	testByte := []byte{0x00}
	if n, err := port.WriteBytes(testByte); err != nil {
		log.Printf("[ezsp] WARNING: Could not write to serial port: %v\n", err)
	} else {
		log.Printf("[ezsp] Serial port write test: wrote %d byte(s)\n", n)
	}
	
	// Some EZSP devices (like Sonoff Dongle Max) may need a reset on connect
	// Try to reset the device to ensure it's in a known state
	log.Printf("[ezsp] Attempting hardware reset (nRESET) to wake device...\n")
	if err := port.ResetDevice(); err != nil && settings.LogErrors {
		log.Printf("[ezsp] Warning: could not reset device: %v\n", err)
	}
	
	// Give device time to initialize after reset
	// Some devices need more time, especially if power was just applied or after wake-up
	log.Printf("[ezsp] Waiting for device to initialize after reset...\n")
	time.Sleep(1 * time.Second) // Increased wait time for wake-up
	
	// Try sending a wake-up sequence - some devices need data to wake up
	// Send a few null bytes or try to "ping" the device
	log.Printf("[ezsp] Sending wake-up sequence...\n")
	for i := 0; i < 3; i++ {
		port.WriteBytes([]byte{0x00})
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	
	// Check if device is in bootloader mode or booting
	log.Printf("[ezsp] Checking device state (bootloader vs application mode)...\n")
	inBootloader, bootloaderData, err := port.CheckBootloaderMode()
	if err != nil {
		log.Printf("[ezsp] Error checking bootloader mode: %v\n", err)
	} else if inBootloader {
		log.Printf("[ezsp] Device detected in bootloader/booting state\n")
		log.Printf("[ezsp] Boot data received: %q\n", bootloaderData)
		
		// Check if this is ESP32 boot messages (Sonoff Dongle Max uses ESP32)
		if strings.Contains(strings.ToLower(string(bootloaderData)), "ets ") {
			log.Printf("[ezsp] ESP32 boot messages detected - waiting for boot to complete...\n")
			log.Printf("[ezsp] The device should automatically boot into application mode\n")
			
			// Wait for boot to complete
			bootData, bootErr := port.WaitForBootCompletion(5 * time.Second)
			if bootErr != nil {
				log.Printf("[ezsp] Warning: Boot completion check failed: %v\n", bootErr)
			} else if len(bootData) > len(bootloaderData) {
				log.Printf("[ezsp] Additional boot data received: %d more bytes\n", len(bootData)-len(bootloaderData))
			}
			
			// ESP32 may need time to initialize the EZSP bridge after boot
			// The EZSP chip might be on a different UART that the ESP32 bridges
			log.Printf("[ezsp] Waiting for ESP32 to initialize EZSP bridge...\n")
			log.Printf("[ezsp] IMPORTANT: Sonoff Dongle Max may require button press to activate EZSP interface!\n")
			log.Printf("[ezsp] If device doesn't respond, try pressing the button on top of the device.\n")
			time.Sleep(2 * time.Second) // Increased wait time for bridge initialization
			
			// Try to "ping" the EZSP interface - send a few bytes to see if it's ready
			log.Printf("[ezsp] Testing EZSP interface readiness...\n")
			for i := 0; i < 3; i++ {
				port.WriteBytes([]byte{0x00})
				time.Sleep(100 * time.Millisecond)
				// Check if we get any response
				testData, _ := port.ReadRawBytes(200*time.Millisecond, 64)
				if len(testData) > 0 {
					log.Printf("[ezsp] Received response after ping: %X (%q)\n", testData, testData)
					// Check if it's an ASH frame
					for _, b := range testData {
						if b == ASH_FLAG {
							log.Printf("[ezsp] ASH frame detected - EZSP interface is active!\n")
							break
						}
					}
				}
			}
			time.Sleep(500 * time.Millisecond)
		} else {
			log.Printf("[ezsp] WARNING: Device appears to be in BOOTLOADER mode!\n")
			log.Printf("[ezsp] Device needs to be flashed with application firmware.\n")
			log.Printf("[ezsp] You may need to:\n")
			log.Printf("[ezsp]   1. Use Simplicity Studio to flash firmware\n")
			log.Printf("[ezsp]   2. Use the device's web interface to update firmware\n")
			log.Printf("[ezsp]   3. Use a firmware update tool specific to your device\n")
		}
	} else if len(bootloaderData) > 0 {
		log.Printf("[ezsp] Device sent data but doesn't appear to be in bootloader mode\n")
		log.Printf("[ezsp] Received data: %X (%q)\n", bootloaderData, bootloaderData)
	} else {
		log.Printf("[ezsp] Device appears ready (no bootloader data detected)\n")
	}
	
	return c, nil
}

func (c *Controller) readLoop(messages chan<- zigbee.IncomingMessage) {
	defer close(c.readLoopDone)
	
	for {
		rawFrame, err := c.port.ReadASHFrame()
		if err != nil {
			if err == io.EOF {
				return
			}
			if c.settings.LogErrors {
				log.Printf("[ezsp] Error reading ASH frame: %v\n", err)
			}
			continue
		}
		
		ashFrame, err := ParseASHFrame(rawFrame)
		if err != nil {
			if c.settings.LogErrors {
				log.Printf("[ezsp] Error parsing ASH frame: %v (raw: %X)\n", err, rawFrame)
			}
			continue
		}
		
		if c.settings.LogCommands {
			log.Printf("[ezsp] ASH Frame: %s\n", ashFrame.DebugString())
		}
		
		switch ashFrame.Type {
		case ASH_FRAME_RSTACK:
			// Handshake complete - this is handled in Start()
			
		case ASH_FRAME_DATA:
			// Parse EZSP frame from ASH DATA frame
			ezspFrame, err := ParseEZSPFrame(ashFrame.Data)
			if err != nil {
				if c.settings.LogErrors {
					log.Printf("[ezsp] Error parsing EZSP frame: %v\n", err)
				}
				continue
			}
			
			if c.settings.LogCommands {
				log.Printf("[ezsp] EZSP Frame: Seq=%d, Control=0x%02X, ID=0x%02X, Params=%X\n",
					ezspFrame.Sequence, ezspFrame.Control, ezspFrame.FrameID, ezspFrame.Parameters)
			}
			
			// Handle response frames
			if ezspFrame.Control == EZSP_FRAME_CONTROL_RESPONSE {
				c.handlerMux.Lock()
				handler := c.handlers[ezspFrame.Sequence]
				if handler != nil {
					delete(c.handlers, ezspFrame.Sequence)
				}
				c.handlerMux.Unlock()
				
				if handler != nil {
					handler.fulfill(ezspFrame)
				} else {
					// Handle callbacks (incoming messages, etc.)
					c.handleCallback(ezspFrame, messages)
				}
			}
			
		case ASH_FRAME_ACK, ASH_FRAME_NAK:
			// ACK/NAK handling - for now we just log
			if c.settings.LogCommands {
				log.Printf("[ezsp] Received %v for AckNum=%d\n", ashFrame.Type, ashFrame.AckNum)
			}
		}
	}
}

func (c *Controller) handleCallback(frame *EZSPFrame, messages chan<- zigbee.IncomingMessage) {
	switch frame.FrameID {
	case EZSP_INCOMING_MESSAGE_HANDLER:
		// TODO: Parse incoming message and send to messages channel
		if c.settings.LogCommands {
			log.Printf("[ezsp] Incoming message callback (not yet implemented)\n")
		}
	}
}

func (c *Controller) Close() error {
	return c.port.Close()
}

// PerformRSTHandshake performs the ASH RST/RSTACK handshake.
// This function can be called independently to verify RST handling.
// Some EZSP devices (like Sonoff Dongle Max) may send an RST frame automatically
// when first connected, so we check for that first.
func (c *Controller) PerformRSTHandshake() error {
	// Step 0: Drain any pending data and check for device-initiated RST
	// Some EZSP devices (like Sonoff Dongle Max) send RST automatically on connect
	// NOTE: Sonoff Dongle Max has a button that may need to be pressed to wake the device
	log.Printf("[ezsp] Starting ASH handshake - checking for device-initiated RST...\n")
	log.Printf("[ezsp] NOTE: If device LED is yellow/orange, it may indicate insufficient power.\n")
	log.Printf("[ezsp] Dongle Max requires 5V@1A via USB - ensure adequate power supply.\n")
	log.Printf("[ezsp] ============================================================\n")
	log.Printf("[ezsp] IMPORTANT: Sonoff Dongle Max requires button press to activate!\n")
	log.Printf("[ezsp] Press the button on top of the device NOW if you haven't already.\n")
	log.Printf("[ezsp] The button activates the EZSP interface after ESP32 boot.\n")
	log.Printf("[ezsp] ============================================================\n")
	time.Sleep(2 * time.Second) // Give user time to press the button
	
	// Check if device sends RST automatically (non-blocking check)
	rawFrame, err := c.port.ReadASHFrameWithTimeout(500 * time.Millisecond)
	if err == nil {
		ashFrame, parseErr := ParseASHFrame(rawFrame)
		if parseErr == nil && ashFrame.Type == ASH_FRAME_RST {
			log.Printf("[ezsp] Device sent RST automatically: %X\n", rawFrame)
		} else if parseErr != nil {
			log.Printf("[ezsp] Received data but not RST: %v (raw: %X)\n", parseErr, rawFrame)
		}
	} else if err != io.ErrNoProgress {
		log.Printf("[ezsp] No device-initiated RST received (this is normal if device doesn't auto-send)\n")
	}
	
	// Step 1: Send RST frame to initiate ASH handshake
	// (Even if device sent RST, we send our own to establish the session)
	rst := buildRSTFrame()
	log.Printf("[ezsp] Sending RST frame: %X\n", rst)
	if _, err := c.port.WriteBytes(rst); err != nil {
		return fmt.Errorf("sending RST frame: %w", err)
	}
	log.Printf("[ezsp] RST frame sent, waiting for RSTACK response...\n")
	
	// For ESP32-bridged devices, may need more time for the bridge to forward the frame
	time.Sleep(200 * time.Millisecond)
	
	// Step 2: Wait for RSTACK (with timeout)
	// First, try to read any raw bytes to see what's coming from the device
	// This helps diagnose if the device is sending anything at all
	log.Printf("[ezsp] Checking for any incoming data from device...\n")
	
	// For ESP32-bridged devices, the EZSP chip might need activation
	// Try reading immediately after sending RST to catch any quick response
	rawBytes, err := c.port.ReadRawBytes(500*time.Millisecond, 256)
	if err == nil && len(rawBytes) > 0 {
		log.Printf("[ezsp] Received raw bytes from device: %X (length: %d)\n", rawBytes, len(rawBytes))
		log.Printf("[ezsp] Raw bytes as ASCII (if printable): %q\n", rawBytes)
		// Check if any bytes are flag bytes
		flagCount := 0
		for _, b := range rawBytes {
			if b == ASH_FLAG {
				flagCount++
			}
		}
		if flagCount > 0 {
			log.Printf("[ezsp] Found %d flag bytes (0x7E) in received data - ASH frames detected!\n", flagCount)
		} else {
			log.Printf("[ezsp] Device sent data but no ASH flag bytes found\n")
			log.Printf("[ezsp] This might be ESP32 bridge data or the EZSP chip isn't active yet\n")
		}
	} else if err == io.ErrNoProgress {
		log.Printf("[ezsp] No immediate response from device\n")
		// Try one more time with longer timeout
		log.Printf("[ezsp] Waiting longer for device response...\n")
		rawBytes, err = c.port.ReadRawBytes(1*time.Second, 256)
		if err == nil && len(rawBytes) > 0 {
			log.Printf("[ezsp] Received delayed response: %X (%q)\n", rawBytes, rawBytes)
		} else {
			log.Printf("[ezsp] No data received from device - device appears silent\n")
		}
	} else if err != nil {
		log.Printf("[ezsp] Error reading raw bytes: %v\n", err)
	}
	
	rstackCh := make(chan error, 1)
	go func() {
		attempts := 0
		for {
			attempts++
			// Use timeout to avoid blocking forever
			rawFrame, err := c.port.ReadASHFrameWithTimeout(11 * time.Second)
			if err != nil {
				if err == io.EOF {
					rstackCh <- fmt.Errorf("connection closed")
					return
				}
				if err == io.ErrNoProgress {
					// Timeout - no flag byte received
					// Before giving up, try reading raw bytes one more time to see if device sends anything
					if attempts == 1 {
						log.Printf("[ezsp] No flag byte received, checking for any raw data...\n")
						rawCheck, checkErr := c.port.ReadRawBytes(500*time.Millisecond, 256)
						if checkErr == nil && len(rawCheck) > 0 {
							log.Printf("[ezsp] Device IS sending data, but not in ASH format: %X\n", rawCheck)
							log.Printf("[ezsp] This might indicate:\n")
							log.Printf("[ezsp]   - Device is in bootloader mode\n")
							log.Printf("[ezsp]   - Device uses different protocol\n")
							log.Printf("[ezsp]   - Device needs firmware update\n")
						} else {
							log.Printf("[ezsp] No raw data received either - device appears completely silent\n")
						}
					}
					log.Printf("[ezsp] Timeout waiting for flag byte (0x7E) - no ASH frame received\n")
					log.Printf("[ezsp] Troubleshooting suggestions:\n")
					log.Printf("[ezsp]   1. Verify device is powered and connected\n")
					log.Printf("[ezsp]   2. Check if device needs firmware update\n")
					log.Printf("[ezsp]   3. Try unplugging and replugging the device\n")
					log.Printf("[ezsp]   4. Verify baud rate is 115200\n")
					log.Printf("[ezsp]   5. Check if device is in bootloader mode\n")
					log.Printf("[ezsp]   6. Verify serial port permissions (user in dialout group?)\n")
					log.Printf("[ezsp]   7. Check if another process is using the serial port\n")
					rstackCh <- fmt.Errorf("timeout: device not responding (no ASH frame received after %d attempt(s))", attempts)
					return
				}
				if c.settings.LogErrors {
					log.Printf("[ezsp] Error reading frame during handshake (attempt %d): %v\n", attempts, err)
				}
				continue
			}
			
			if c.settings.LogCommands {
				log.Printf("[ezsp] Received raw frame during handshake (attempt %d): %X\n", attempts, rawFrame)
			}
			
			ashFrame, err := ParseASHFrame(rawFrame)
			if err != nil {
				if c.settings.LogErrors {
					log.Printf("[ezsp] Failed to parse frame during handshake (attempt %d): %v (raw: %X)\n", attempts, err, rawFrame)
				}
				continue
			}
			
			if c.settings.LogCommands {
				log.Printf("[ezsp] Parsed frame during handshake (attempt %d): Type=%v, Control=0x%02X\n", attempts, ashFrame.Type, ashFrame.Control)
			}
			
			if ashFrame.Type == ASH_FRAME_RSTACK {
				// Handshake complete
				if c.settings.LogCommands {
					log.Printf("[ezsp] ASH handshake complete\n")
				}
				rstackCh <- nil
				return
			} else if ashFrame.Type == ASH_FRAME_RST {
				// Device sent RST back - might be responding to our RST
				if c.settings.LogCommands {
					log.Printf("[ezsp] Device sent RST frame, continuing to wait for RSTACK\n")
				}
			} else {
				if c.settings.LogCommands {
					log.Printf("[ezsp] Unexpected frame type during handshake: %v\n", ashFrame.Type)
				}
			}
		}
	}()
	
	select {
	case err := <-rstackCh:
		return err
	case <-time.After(12 * time.Second):
		return fmt.Errorf("timeout waiting for RSTACK after sending RST frame (device may not be responding)")
	}
}

func (c *Controller) Start() (chan zigbee.IncomingMessage, error) {
	// Step 1: Perform RST/RSTACK handshake
	if err := c.PerformRSTHandshake(); err != nil {
		return nil, err
	}
	
	// Step 3: Initialize EZSP protocol - send version command
	versionFrame := &EZSPFrame{
		Sequence:   c.port.NextSequence(),
		Control:    EZSP_FRAME_CONTROL_COMMAND,
		FrameID:    EZSP_VERSION,
		Parameters: []byte{},
	}
	
	ezspData := SerializeEZSPFrame(versionFrame)
	control := byte(0x00) // DATA frame control: FrameNum=0, AckNum=0, ReTx=0
	ashDataFrame := BuildASHDataFrame(control, ezspData)
	
	if _, err := c.port.WriteBytes(ashDataFrame); err != nil {
		return nil, fmt.Errorf("sending version command: %w", err)
	}
	
	// Read version response manually (before starting readLoop)
	versionResponseCh := make(chan *EZSPFrame, 1)
	versionErrorCh := make(chan error, 1)
	go func() {
		for {
			rawFrame, err := c.port.ReadASHFrame()
			if err != nil {
				versionErrorCh <- err
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
				
				// Check if this is the version response
				if ezspFrame.Control == EZSP_FRAME_CONTROL_RESPONSE &&
					ezspFrame.Sequence == versionFrame.Sequence &&
					ezspFrame.FrameID == EZSP_VERSION {
					versionResponseCh <- ezspFrame
					return
				}
			}
		}
	}()
	
	select {
	case response := <-versionResponseCh:
		version, err := ParseVersionResponse(response.Parameters)
		if err != nil {
			return nil, fmt.Errorf("parsing version response: %w", err)
		}
		
		if c.settings.LogCommands {
			log.Printf("[ezsp] Protocol version: %d, Stack type: %d, Stack version: %d\n",
				version.ProtocolVersion, version.StackType, version.StackVersion)
		}
	case err := <-versionErrorCh:
		return nil, fmt.Errorf("reading version response: %w", err)
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout waiting for version response")
	}
	
	// Step 4: Start read loop for handling frames
	messages := make(chan zigbee.IncomingMessage, 10)
	go c.readLoop(messages)
	
	return messages, nil
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

func (c *Controller) Send(message zigbee.OutgoingMessage) error {
	// TODO: Implement EZSP_SEND_UNICAST command
	return fmt.Errorf("not yet implemented")
}

func (c *Controller) PermitJoining(enabled bool) error {
	// TODO: Implement EZSP_PERMIT_JOINING command
	return fmt.Errorf("not yet implemented")
}

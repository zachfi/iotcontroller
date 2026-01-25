package znp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/jacobsa/go-serial/serial"
	"golang.org/x/sys/unix"
)

type ErrorHandling int

const (
	ErrorHandlingPanic    ErrorHandling = 0
	ErrorHandlingStop     ErrorHandling = 1
	ErrorHandlingContinue ErrorHandling = 2
)

type Callbacks struct {
	BeforeWrite func(command interface{})
	AfterRead   func(command interface{})

	OnReadError  func(err error) ErrorHandling
	OnParseError func(err error, frame Frame) ErrorHandling
}

var ErrTimeout = errors.New("command timed out")

type handlerResult struct {
	command interface{}
	err     error
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

func (h *Handler) fulfill(command interface{}) {
	h.results <- handlerResult{command, nil}
	if h.timer != nil {
		h.timer.Stop()
	}
}

func (h *Handler) Receive() (interface{}, error) {
	result := <-h.results
	return result.command, result.err
}

type Port struct {
	sp     io.ReadWriteCloser
	reader *bufio.Reader // Buffered reader for continuous byte reading
	cbs    Callbacks

	handlerMutex sync.Mutex
	handlers     map[FrameHeader]*Handler
	fd           *os.File // File descriptor for ioctl operations (Linux only)

	closeOnce sync.Once
	done      chan struct{}
}

func NewPort(name string, baudRate int, disableFlowControl bool, callbacks Callbacks) (*Port, error) {
	if baudRate == 0 {
		baudRate = 115200 // Default
	}
	options := serial.OpenOptions{
		PortName:          name,
		BaudRate:          uint(baudRate),
		RTSCTSFlowControl: !disableFlowControl, // Enable unless explicitly disabled
		DataBits:          8,
		StopBits:          1,
		MinimumReadSize:   1,
		// Note: InterCharacterTimeout is NOT set in the original working code
		// Setting it may interfere with blocking reads
		//
		// IMPORTANT: Flow control (RTS/CTS) is critical for reliable communication.
		// Without it, the device can send data faster than we can read, causing:
		// - Serial port buffer overruns
		// - Dropped bytes
		// - Incomplete frames (e.g., 13 bytes when 19 are expected)
		// - Corrupted frames
		// Only disable flow control if the device explicitly requires it (some Z-Stack 3.x devices).
	}
	if disableFlowControl {
		fmt.Printf("[znp] Flow control DISABLED (RTS/CTS off)\n")
	} else {
		fmt.Printf("[znp] Flow control ENABLED (RTS/CTS on)\n")
	}

	sp, err := serial.Open(options)
	if err != nil {
		return nil, err
	}

	port := &Port{
		sp:     sp,
		reader: bufio.NewReaderSize(sp, 4096), // Increased buffer size to reduce overruns when flow control is disabled
		cbs:    callbacks,

		handlerMutex: sync.Mutex{},
		handlers:     make(map[FrameHeader]*Handler),
		done:         make(chan struct{}),
	}

	// Try to get file descriptor for ioctl operations (Linux)
	// The serial library returns *os.File on Linux, which we can use for ioctl
	if file, ok := sp.(*os.File); ok {
		port.fd = file
	}

	// Start the continuous read loop
	go port.loop()

	// Give the read loop a moment to start before returning
	// This ensures byteReader and frameParser are ready before commands are sent
	time.Sleep(10 * time.Millisecond)

	return port, nil
}

// BufferedReadByte reads a single byte from the buffered reader.
// This is used by the continuous byte reader goroutine.
func (p *Port) BufferedReadByte() (byte, error) {
	return p.reader.ReadByte()
}

func (p *Port) Close() error {
	var err error
	p.closeOnce.Do(func() {
		// Close the serial port - this will cause readFrame() to return os.ErrClosed
		// which will cause the loop to exit when it checks for os.ErrClosed
		err = p.sp.Close()

		// Signal that we're closing - loop will exit on next iteration check
		close(p.done)

		// Fail all pending handlers so they don't block forever
		p.handlerMutex.Lock()
		for _, handler := range p.handlers {
			handler.fail()
		}
		p.handlers = make(map[FrameHeader]*Handler)
		p.handlerMutex.Unlock()
	})
	return err
}

func (p *Port) RegisterOneOffHandler(commandPrototype interface{}) *Handler {
	header := getHeaderForCommand(commandPrototype)
	// Use 30 second timeout for state change operations (Z-Stack 3.x may need more time)
	return p.registerHandler(header, 30*time.Second)
}

func (p *Port) RegisterPermanentHandler(commandPrototype interface{}) *Handler {
	header := getHeaderForCommand(commandPrototype)
	return p.registerHandler(header, 0)
}

func (p *Port) registerHandler(header FrameHeader, timeout time.Duration) *Handler {
	handler := newHandler()

	p.handlerMutex.Lock()
	defer p.handlerMutex.Unlock()

	if _, ok := p.handlers[header]; ok {
		panic(fmt.Sprintf("handler for %v already exists", header))
	}
	p.handlers[header] = handler

	if timeout != 0 {
		handler.timer = time.AfterFunc(timeout, func() {
			p.removeHandler(handler, header)
		})
	}

	return handler
}

func (p *Port) removeHandler(handler *Handler, header FrameHeader) {
	found := false
	p.handlerMutex.Lock()
	if p.handlers[header] == handler {
		delete(p.handlers, header)
		found = true
	}
	p.handlerMutex.Unlock()

	if found {
		handler.fail()
	}
}

// ResetDevice performs a hardware reset by toggling DTR line.
// This is often needed before sending the magic byte to ensure the device
// is in a known state. Similar to EZSP devices, Z-Stack devices may need
// this reset sequence for proper initialization.
func (p *Port) ResetDevice() error {
	fmt.Printf("[znp] ResetDevice() called\n")
	fmt.Printf("[znp] Checking if serial port supports DTR control...\n")

	// First try the serial library's interface if available
	if dtrControl, ok := p.sp.(interface{ SetDTR(bool) error }); ok {
		fmt.Printf("[znp] DTR control available via library interface\n")
		fmt.Printf("[znp] Performing hardware reset via DTR (nRESET)...\n")
		// Assert reset: pull DTR low
		if err := dtrControl.SetDTR(false); err != nil {
			fmt.Printf("[znp] ERROR: failed to assert DTR (reset): %v\n", err)
			return fmt.Errorf("failed to assert DTR (reset): %w", err)
		}
		fmt.Printf("[znp] DTR pulled low, waiting 10ms...\n")
		time.Sleep(10 * time.Millisecond)
		// Release reset: pull DTR high
		if err := dtrControl.SetDTR(true); err != nil {
			fmt.Printf("[znp] ERROR: failed to release DTR (reset): %v\n", err)
			return fmt.Errorf("failed to release DTR (reset): %w", err)
		}
		fmt.Printf("[znp] DTR released high, waiting 50ms for device to stabilize...\n")
		// Wait for device to stabilize after reset
		time.Sleep(50 * time.Millisecond)
		fmt.Printf("[znp] Hardware reset via DTR complete\n")
		return nil
	}

	fmt.Printf("[znp] DTR control NOT available via library interface\n")

	// If library doesn't support it, try direct ioctl on Linux
	if p.fd != nil {
		fmt.Printf("[znp] File descriptor available, trying ioctl...\n")
		return p.resetViaIoctl()
	}

	// If all else fails, it's not fatal - continue without reset
	fmt.Printf("[znp] WARNING: Hardware reset (nRESET) not available - serial library doesn't support DTR/RTS control\n")
	fmt.Printf("[znp] No file descriptor available for ioctl\n")
	fmt.Printf("[znp] Device may need manual reset or may not initialize properly\n")
	return nil
}

// AssertRTS explicitly asserts the RTS line to enable transmission.
// NOTE: When RTSCTSFlowControl is enabled, the kernel automatically manages RTS
// via the CRTSCTS flag. Manual assertion may conflict with kernel management.
// However, some devices may need RTS asserted initially before the kernel
// takes over. This should typically only be called when flow control is disabled,
// or as a workaround for devices that don't work with automatic flow control.
func (p *Port) AssertRTS() error {
	// First try the serial library's interface if available
	if rtsControl, ok := p.sp.(interface{ SetRTS(bool) error }); ok {
		return rtsControl.SetRTS(true)
	}
	// Try ioctl as fallback
	if p.fd != nil {
		var status uint
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMGET, uintptr(unsafe.Pointer(&status)))
		if errno != 0 {
			return fmt.Errorf("could not get modem status: %v", errno)
		}
		status |= unix.TIOCM_RTS
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMSET, uintptr(unsafe.Pointer(&status)))
		if errno != 0 {
			return fmt.Errorf("could not set RTS: %v", errno)
		}
		return nil
	}
	return fmt.Errorf("no RTS control available")
}

// resetViaIoctl performs hardware reset using Linux ioctl to control DTR line.
func (p *Port) resetViaIoctl() error {
	fmt.Printf("[znp] Attempting hardware reset via ioctl (DTR control)...\n")

	// Get current modem status
	var status uint
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMGET, uintptr(unsafe.Pointer(&status)))
	if errno != 0 {
		return fmt.Errorf("could not get modem status via ioctl: %v", errno)
	}

	// Assert reset: Clear DTR bit (pull DTR low)
	status &^= unix.TIOCM_DTR
	_, _, errno = unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMSET, uintptr(unsafe.Pointer(&status)))
	if errno != 0 {
		return fmt.Errorf("could not set DTR low via ioctl: %v", errno)
	}

	// Hold reset for at least 1ms (recommended 1-10ms)
	time.Sleep(10 * time.Millisecond)

	// Release reset: Set DTR bit (pull DTR high)
	status |= unix.TIOCM_DTR
	_, _, errno = unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMSET, uintptr(unsafe.Pointer(&status)))
	if errno != 0 {
		return fmt.Errorf("could not set DTR high via ioctl: %v", errno)
	}

	// Wait for device to stabilize after reset
	time.Sleep(50 * time.Millisecond)
	fmt.Printf("[znp] Hardware reset via ioctl complete\n")
	return nil
}

func (p *Port) WriteMagicByteForBootloader() error {
	// Send magic byte to bootloader, to skip 60s wait after startup.
	//
	// "I found the solution.
	//  The serial boot loader is waiting for 60 seconds before jumping to the ZNP application image, as documented in Serial Boot Loader document.
	//  Sending a magic byte 0xef can force the sbl to skip the waiting.
	//  However, the version in Home-1.2.1 changed the magic byte from 0x10 to 0xef, and the document did not update this.
	//  That's why I could not get it work before.
	//  Also the response to the magic byte has been changed from 0x00 to SYS_RESET_IND. again no document update."
	//
	//    - Kyle Zhou in [2]
	//
	// See: [1] https://github.com/Koenkk/zigbee2mqtt/issues/1343
	// See: [2] https://e2e.ti.com/support/wireless-connectivity/zigbee-and-thread/f/158/p/160948/1361000#1361000
	//
	_, err := p.sp.Write([]byte{0xEF})
	return err
}

func (p *Port) WriteCommand(command interface{}) (interface{}, error) {
	return p.WriteCommandTimeout(command, 1*time.Second)
}

func (p *Port) WriteCommandTimeout(command interface{}, timeout time.Duration) (interface{}, error) {
	if p.cbs.BeforeWrite != nil {
		p.cbs.BeforeWrite(command)
	}

	frame := buildFrameForCommand(command)

	var handler *Handler
	var responseHeader FrameHeader

	if frame.Type == FRAME_TYPE_SREQ {
		responseHeader = frame.FrameHeader
		responseHeader.Type = FRAME_TYPE_SRSP
		// Register handler BEFORE writing the frame to ensure it's ready when response arrives
		handler = p.registerHandler(responseHeader, timeout)
		// Log handler registration for debugging
		if p.cbs.OnReadError != nil {
			fmt.Printf("[znp] WriteCommandTimeout: registered handler for response %v (timeout: %v)\n", responseHeader, timeout)
		}
		// Small delay to ensure handler is fully registered and frame processing loop is ready
		// This prevents race conditions where response arrives before handler is set up
		time.Sleep(1 * time.Millisecond)
	}

	err := writeFrame(p.sp, frame)
	if err != nil {
		// If write fails, clean up the handler
		if handler != nil {
			p.handlerMutex.Lock()
			delete(p.handlers, responseHeader)
			p.handlerMutex.Unlock()
			handler.fail()
		}
		return nil, err
	}

	if handler != nil {
		return handler.Receive()
	}

	return nil, nil
}

// loop orchestrates two stages: chunk reading with buffered parsing, and frame handling.
// This matches zigbee-herdsman's approach: read chunks, accumulate in buffer, parse when complete.
func (p *Port) loop() {
	// Stage 1: Chunk reader + Frame parser (combined for efficiency)
	// Read chunks from serial port and parse frames from accumulated buffer
	frameCh := make(chan Frame, 32) // Buffered for complete frames
	parserDone := make(chan struct{})

	// Start combined reader/parser (Stage 1)
	go p.chunkReaderAndParser(frameCh, parserDone)

	// Stage 2: Frame handler - processes complete ZNP frames (main loop)
	for {
		select {
		case <-p.done:
			// Wait for parser to finish
			<-parserDone
			return
		case frame, ok := <-frameCh:
			if !ok {
				// Parser closed channel (error or done)
				return
			}

			// Process the frame
			p.handleFrame(frame)
		}
	}
}

// chunkReaderAndParser reads chunks from the serial port and parses frames from an accumulated buffer.
// This matches zigbee-herdsman's approach: accumulate chunks, parse when frames are complete.
// This is more efficient than byte-by-byte processing and reduces channel overhead.
func (p *Port) chunkReaderAndParser(frameCh chan<- Frame, done chan<- struct{}) {
	defer close(done)
	defer close(frameCh)

	// Accumulated buffer for frame parsing (like zigbee-herdsman's parser.buffer)
	var buffer []byte
	chunkBuf := make([]byte, 256) // Read chunks of 256 bytes

	for {
		select {
		case <-p.done:
			return
		default:
		}

		// Read a chunk from the serial port
		n, err := p.reader.Read(chunkBuf)
		if err != nil {
			if errors.Is(err, os.ErrClosed) || err == io.EOF {
				return
			}
			if p.cbs.OnReadError != nil {
				handling := p.cbs.OnReadError(err)
				if handling == ErrorHandlingStop || handling == ErrorHandlingPanic {
					return
				}
				// Continue reading on ErrorHandlingContinue
				continue
			}
			// Continue reading on non-fatal errors
			continue
		}

		if n == 0 {
			continue
		}

		// Accumulate the chunk in our buffer (like zigbee-herdsman: this.buffer = Buffer.concat([this.buffer, chunk]))
		buffer = append(buffer, chunkBuf[:n]...)

		// Parse frames from the buffer (may parse multiple frames if buffer has multiple complete frames)
		buffer = p.parseFramesFromBuffer(buffer, frameCh)
	}
}

// parseFramesFromBuffer parses complete ZNP frames from the accumulated buffer.
// This matches zigbee-herdsman's parseNext() approach: check if we have enough data for a frame,
// parse it if complete, remove it from buffer, and recurse to parse more frames.
// Returns the remaining buffer (incomplete frame data or empty if all frames were parsed).
func (p *Port) parseFramesFromBuffer(buffer []byte, frameCh chan<- Frame) []byte {
	// Keep parsing frames while we have complete ones in the buffer
	for {
		// Skip garbage bytes until we find SOF (like zigbee-herdsman)
		if len(buffer) > 0 && buffer[0] != SOF {
			// Find first SOF
			index := -1
			for i, b := range buffer {
				if b == SOF {
					index = i
					break
				}
			}
			if index >= 0 {
				// Discard garbage before SOF
				buffer = buffer[index:]
			} else {
				// No SOF found - wait for more data
				return buffer
			}
		}

		// Check if we have minimum frame length (SOF + Length + Command + FCS = 5 bytes minimum)
		const MinMessageLength = 5
		if len(buffer) < MinMessageLength || buffer[0] != SOF {
			// Not enough data or not starting with SOF - wait for more
			return buffer
		}

		// Read data length (byte at position 1, after SOF)
		// Position matches zigbee-herdsman's PositionDataLength = 1
		dataLength := int(buffer[1])
		if dataLength > FRAME_MAX_DATA_LENGTH {
			// Invalid length - skip this byte and try again
			if p.cbs.OnReadError != nil {
				fmt.Printf("[znp] Frame parser: ERROR - invalid length byte 0x%02X (%d), max is %d. Skipping.\n", buffer[1], dataLength, FRAME_MAX_DATA_LENGTH)
			}
			buffer = buffer[1:] // Skip the invalid byte
			continue
		}

		// Calculate frame positions (matching zigbee-herdsman's constants)
		// DataStart = 4 (SOF + Length + Command = 1 + 1 + 2)
		const DataStart = 4
		fcsPosition := DataStart + dataLength
		frameLength := fcsPosition + 1 // Include FCS byte

		// Check if we have enough data for a complete frame
		if len(buffer) >= frameLength {
			// Extract the complete frame
			frameBytes := buffer[:frameLength]

			// Try to parse it (using zigbee-herdsman's frame structure)
			frame, err := parseFrameFromBytes(frameBytes)
			if err == nil {
				// Valid frame - send it
				if p.cbs.OnReadError != nil {
					fmt.Printf("[znp] Frame parser: extracted frame Type=%v Subsystem=%v ID=%v DataLen=%d\n",
						frame.Type, frame.Subsystem, frame.ID, len(frame.Data))
				}
				select {
				case frameCh <- frame:
				case <-p.done:
					return buffer
				}
				// Remove parsed frame from buffer and continue parsing
				buffer = buffer[frameLength:]
				// Recurse to parse more frames (like zigbee-herdsman's parseNext recursion)
				continue
			} else {
				// Parse failed - skip the SOF byte and try again
				if p.cbs.OnReadError != nil {
					fmt.Printf("[znp] Frame parser: failed to parse frame: %v, hex: %X\n", err, frameBytes)
				}
				buffer = buffer[1:] // Skip the SOF and try again
				continue
			}
		} else {
			// Not enough data for complete frame - wait for more chunks
			// This is the key difference from our old approach: we DON'T discard, we wait!
			// This matches zigbee-herdsman's behavior - incomplete frames stay in buffer
			return buffer
		}
	}
}

// frameParser reads bytes from byteCh, maintains a buffer, and extracts complete ZNP frames.
// DEPRECATED: This byte-by-byte approach is less efficient. Use chunkReaderAndParser instead.
// Kept for reference but should be removed once chunkReaderAndParser is proven.
func (p *Port) frameParser(byteCh <-chan byte, frameCh chan<- Frame, done chan<- struct{}) {
	defer close(done)
	defer close(frameCh)

	var currentFrame []byte
	var dataLength int  // Length of data field (from length byte)
	var pendingSOF byte // Buffer for SOF byte if we see one while reading a frame

	for {
		select {
		case <-p.done:
			return
		case b, ok := <-byteCh:
			if !ok {
				// Byte reader closed channel
				return
			}

			if b == SOF {
				// Start of new frame
				if len(currentFrame) > 0 && dataLength > 0 {
					// We're already in a frame - check if it's complete before resetting
					expectedTotal := dataLength + 5
					if len(currentFrame) >= expectedTotal {
						// We have a complete frame - process it before starting new one
						frameBytes := currentFrame[:expectedTotal]
						frame, err := parseFrameFromBytes(frameBytes)
						if err == nil {
							// Valid complete frame - send it
							if p.cbs.OnReadError != nil {
								fmt.Printf("[znp] Frame parser: complete frame detected before new SOF, extracting it\n")
							}
							select {
							case frameCh <- frame:
							case <-p.done:
								return
							}
							// Reset for new frame
							currentFrame = []byte{SOF}
							dataLength = 0
							continue
						}
					}
					// Incomplete frame - a second SOF arrived before the first frame was complete.
					// This should not happen in normal operation. The vendor implementation uses
					// io.ReadFull which reads the entire frame atomically, so it never encounters
					// this situation.
					//
					// The hex dump shows we're reading bytes from the second frame (0xFF is the
					// length byte of the second frame) and incorrectly adding them to the first frame.
					// This means the first frame is actually incomplete/corrupted, or bytes were dropped.
					//
					// Strategy: Discard the incomplete first frame and start fresh with the new SOF.
					// This matches the vendor's behavior of discarding corrupted/incomplete frames.
					if p.cbs.OnReadError != nil {
						fmt.Printf("[znp] Frame parser: WARNING - detected SOF while in incomplete frame (len=%d, expected=%d, hex: %X). Discarding incomplete frame and starting fresh.\n", len(currentFrame), dataLength+5, currentFrame)
					}
					// Discard incomplete frame and start fresh with this SOF
					currentFrame = []byte{SOF}
					dataLength = 0
					pendingSOF = 0
					continue
				}
				// Start new frame
				if p.cbs.OnReadError != nil {
					fmt.Printf("[znp] Frame parser: detected SOF, starting new frame\n")
				}
				currentFrame = []byte{SOF}
				dataLength = 0
				pendingSOF = 0
				continue
			}

			if len(currentFrame) == 0 {
				// Not in a frame yet - check if we have a pending SOF
				if pendingSOF == SOF {
					// Use the pending SOF to start a new frame
					currentFrame = []byte{SOF}
					dataLength = 0
					pendingSOF = 0
					// Continue to add this byte to the frame
				} else {
					// Skip garbage bytes until we find SOF
					continue
				}
			}

			// We're in a frame - add byte
			currentFrame = append(currentFrame, b)

			if p.cbs.OnReadError != nil && pendingSOF == SOF {
				// Debug: we're reading bytes for the current frame while we have a pending SOF
				expectedTotal := dataLength + 5
				fmt.Printf("[znp] Frame parser: reading byte 0x%02X for current frame (len=%d/%d, pendingSOF buffered, currentFrame hex: %X)\n", b, len(currentFrame), expectedTotal, currentFrame)
			}

			if len(currentFrame) == 2 {
				// We just read the length byte (after SOF)
				dataLength = int(b)
				// Validate length byte - maximum is FRAME_MAX_DATA_LENGTH (250)
				if dataLength > FRAME_MAX_DATA_LENGTH {
					// Invalid length byte - this is likely corrupted data
					if p.cbs.OnReadError != nil {
						fmt.Printf("[znp] Frame parser: ERROR - invalid length byte 0x%02X (%d), max is %d. Discarding frame and waiting for next SOF.\n", b, dataLength, FRAME_MAX_DATA_LENGTH)
					}
					// Reset and wait for next SOF
					currentFrame = nil
					dataLength = 0
					pendingSOF = 0
					continue
				}
				if p.cbs.OnReadError != nil {
					fmt.Printf("[znp] Frame parser: read length byte: 0x%02X (%d), expecting %d total bytes\n", b, dataLength, dataLength+5)
				}
				continue
			}

			// Check if we've read the complete frame
			// Expected total: 1 (SOF) + 1 (Length) + 2 (Command) + dataLength (Data) + 1 (FCS) = dataLength + 5
			expectedTotal := dataLength + 5
			if p.cbs.OnReadError != nil && pendingSOF == SOF {
				fmt.Printf("[znp] Frame parser: checking frame completion (len=%d, expected=%d)\n", len(currentFrame), expectedTotal)
			}
			if len(currentFrame) >= expectedTotal {
				// Frame is complete - process it
				// If we have a pending SOF, it was the start of the next frame
				if pendingSOF == SOF {
					if p.cbs.OnReadError != nil {
						fmt.Printf("[znp] Frame parser: frame complete, processing pending SOF as start of next frame\n")
					}
				}
				// We've read at least the expected number of bytes
				// Extract exactly the expected number of bytes for this frame
				frameBytes := currentFrame[:expectedTotal]

				// Parse the frame
				frame, err := parseFrameFromBytes(frameBytes)
				if err != nil {
					// Invalid frame - log and discard
					if p.cbs.OnReadError != nil {
						// Log the raw frame bytes for debugging
						p.cbs.OnReadError(fmt.Errorf("failed to parse frame (len=%d, dataLength=%d, expectedTotal=%d): %v, raw bytes: %X", len(frameBytes), dataLength, expectedTotal, err, frameBytes))
						handling := p.cbs.OnReadError(err)
						if handling == ErrorHandlingStop || handling == ErrorHandlingPanic {
							return
						}
					}
					// Reset and wait for next SOF
					currentFrame = nil
					dataLength = 0
					pendingSOF = 0
					continue
				}

				// Send complete frame to handler
				// Log frame details for debugging (if errors enabled)
				if p.cbs.OnReadError != nil {
					// Log all received frames to help debug UtilGetDeviceInfoRequest timeout
					fmt.Printf("[znp] Frame parser: extracted frame Type=%v Subsystem=%v ID=%v DataLen=%d\n",
						frame.Type, frame.Subsystem, frame.ID, len(frame.Data))
				}
				select {
				case frameCh <- frame:
				case <-p.done:
					return
				}

				// If we got more bytes than expected, keep them for the next frame
				if len(currentFrame) > expectedTotal {
					// There might be another frame starting - check if next byte is SOF
					remainingBytes := currentFrame[expectedTotal:]
					if len(remainingBytes) > 0 && remainingBytes[0] == SOF {
						// Next frame starts immediately - start parsing it
						currentFrame = remainingBytes
						dataLength = 0
						pendingSOF = 0
					} else {
						// No SOF found - discard remaining bytes and wait for next SOF
						currentFrame = nil
						dataLength = 0
						pendingSOF = 0
					}
				} else {
					// Exact match - reset for next frame
					// If we have a pending SOF, use it as the start of the next frame
					if pendingSOF == SOF {
						currentFrame = []byte{SOF}
						dataLength = 0
						pendingSOF = 0
					} else {
						currentFrame = nil
						dataLength = 0
					}
				}
			}
			// If len(currentFrame) < expectedTotal, we need more bytes - continue reading
		}
	}
}

// parseFrameFromBytes parses a ZNP frame from a byte slice.
// The slice should contain: SOF + Length + Command (2 bytes) + Data + FCS
// Frame structure matches readFrame() in frame.go:
//   - SOF (1 byte)
//   - Length (1 byte) - length of data field
//   - Command (2 bytes: cmd0, cmd1)
//   - Data (length bytes)
//   - FCS (1 byte)
//
// Total: 1 + 1 + 2 + length + 1 = length + 5 bytes
func parseFrameFromBytes(data []byte) (Frame, error) {
	var frame Frame

	if len(data) < 5 { // Minimum: SOF + Length + Command (2) + FCS
		return frame, ErrInvalidFrame
	}

	if data[0] != SOF {
		return frame, ErrInvalidFrame
	}

	length := int(data[1])
	expectedLength := 1 + 1 + 2 + length + 1 // SOF + Length + Command + Data + FCS
	if len(data) != expectedLength {
		return frame, fmt.Errorf("frame length mismatch: expected %d, got %d", expectedLength, len(data))
	}

	// Verify FCS (matches readFrame logic: FCS is calculated over length + command + data)
	// Buffer for FCS calculation: length + command (2) + data = length + 2 bytes
	fcsBuffer := make([]byte, 1+2+length)   // length + command + data
	fcsBuffer[0] = data[1]                  // length
	copy(fcsBuffer[1:], data[2:2+2+length]) // command + data

	fcs := data[len(data)-1]
	calculatedFCS := calculateFCS(fcsBuffer)
	if fcs != calculatedFCS {
		return frame, ErrInvalidFrame
	}

	// Parse command (matches readFrame logic)
	cmd0, cmd1 := data[2], data[3]
	frame.FrameHeader = FrameHeader{
		Type:      FrameType(cmd0 >> 5),
		Subsystem: FrameSubsystem(cmd0 & 0b00011111),
		ID:        FrameID(cmd1),
	}

	// Extract data (everything between command and FCS)
	if length > 0 {
		frame.Data = make([]byte, length)
		copy(frame.Data, data[4:4+length])
	}

	return frame, nil
}

// handleFrame processes a complete ZNP frame and dispatches it to registered handlers.
func (p *Port) handleFrame(frame Frame) {
	command, err := parseCommandFromFrame(frame)
	if err != nil {
		handling := ErrorHandlingStop
		if p.cbs.OnParseError != nil {
			handling = p.cbs.OnParseError(err, frame)
		}
		if handling == ErrorHandlingContinue {
			return
		} else if handling == ErrorHandlingStop {
			return
		} else {
			panic(err)
		}
	}

	if p.cbs.AfterRead != nil {
		p.cbs.AfterRead(command)
	}

	p.handlerMutex.Lock()
	handler := p.handlers[frame.FrameHeader]
	// Remove one-off handlers (those with timers) after matching
	// Permanent handlers (no timer) remain registered
	if handler != nil && handler.timer != nil {
		delete(p.handlers, frame.FrameHeader)
	}
	// Log all registered handlers for debugging (if errors enabled)
	if p.cbs.OnReadError != nil && frame.Type == FRAME_TYPE_SRSP {
		if handler == nil {
			// List all registered handlers to help debug
			handlerList := make([]string, 0, len(p.handlers))
			for h := range p.handlers {
				handlerList = append(handlerList, fmt.Sprintf("%v", h))
			}
			p.cbs.OnReadError(fmt.Errorf("received SRSP frame %v but no handler registered (registered handlers: %v)", frame.FrameHeader, handlerList))
		}
	}
	p.handlerMutex.Unlock()

	if handler != nil {
		handler.fulfill(command)
	}
	// If handler is nil, this frame doesn't have a registered handler
	// This is normal for unsolicited AREQ frames or frames we're not waiting for
}

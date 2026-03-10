package ember

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jacobsa/go-serial/serial"
	"golang.org/x/sys/unix"
)

type Port struct {
	sp       io.ReadWriteCloser
	reader   *bufio.Reader
	sequence uint32   // Sequence number for EZSP frames
	fd       *os.File // File descriptor for ioctl operations (Linux only)

	closeOnce sync.Once
	done      chan struct{}
}

func NewPort(name string, baudRate int, disableFlowControl bool) (*Port, error) {
	if baudRate == 0 {
		baudRate = 115200 // Default
	}
	options := serial.OpenOptions{
		PortName:          name,
		BaudRate:          uint(baudRate),
		DataBits:          8,
		StopBits:          1,
		ParityMode:        serial.PARITY_NONE,
		RTSCTSFlowControl: !disableFlowControl, // EZSP requires flow control (RTS/CTS) per AN706
		MinimumReadSize:   1,
		// Note: InterCharacterTimeout is NOT set (similar to ZNP)
		// Setting it may interfere with blocking reads
	}

	sp, err := serial.Open(options)
	if err != nil {
		return nil, err
	}

	port := &Port{
		sp:     sp,
		reader: bufio.NewReaderSize(sp, 4096), // Increased buffer size to reduce overruns when flow control is disabled
		done:   make(chan struct{}),
	}

	// Try to get file descriptor for ioctl operations (Linux)
	if file, ok := sp.(*os.File); ok {
		port.fd = file
	}

	// Note: When RTSCTSFlowControl is enabled, the kernel automatically manages RTS
	// via the CRTSCTS flag. We should NOT manually assert RTS as it may conflict
	// with the kernel's automatic flow control handling.

	return port, nil
}

// AssertRTS explicitly asserts the RTS line to enable transmission.
// This should be called after reset to ensure RTS is still asserted.
func (p *Port) AssertRTS() error {
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

// ResetDevice performs a hardware reset by toggling DTR line.
func (p *Port) ResetDevice() error {
	// First try the serial library's interface if available
	if dtrControl, ok := p.sp.(interface{ SetDTR(bool) error }); ok {
		if err := dtrControl.SetDTR(false); err != nil {
			return fmt.Errorf("failed to assert DTR (reset): %w", err)
		}
		time.Sleep(10 * time.Millisecond)
		if err := dtrControl.SetDTR(true); err != nil {
			return fmt.Errorf("failed to release DTR (reset): %w", err)
		}
		time.Sleep(50 * time.Millisecond)
		return nil
	}

	// Try RTS as fallback
	if rtsControl, ok := p.sp.(interface{ SetRTS(bool) error }); ok {
		rtsControl.SetRTS(false)
		time.Sleep(10 * time.Millisecond)
		rtsControl.SetRTS(true)
		time.Sleep(50 * time.Millisecond)
		return nil
	}

	// If library doesn't support it, try direct ioctl on Linux
	if p.fd != nil {
		return p.resetViaIoctl()
	}

	return nil
}

// resetViaIoctl performs hardware reset using Linux ioctl to control DTR line.
func (p *Port) resetViaIoctl() error {
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

	time.Sleep(10 * time.Millisecond)

	// Release reset: Set DTR bit (pull DTR high)
	status |= unix.TIOCM_DTR
	_, _, errno = unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMSET, uintptr(unsafe.Pointer(&status)))
	if errno != 0 {
		return fmt.Errorf("could not set DTR high via ioctl: %v", errno)
	}

	time.Sleep(50 * time.Millisecond)
	return nil
}

func (p *Port) ReadBytes(buf []byte) (int, error) {
	return p.reader.Read(buf)
}

func (p *Port) WriteBytes(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}

	// Write the data
	n, err := p.sp.Write(buf)
	if err != nil {
		return n, fmt.Errorf("write failed: %w", err)
	}

	if n != len(buf) {
		return n, fmt.Errorf("partial write: wrote %d of %d bytes", n, len(buf))
	}

	// Try to flush the write buffer if the underlying connection supports it
	if flusher, ok := p.sp.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return n, fmt.Errorf("flush failed: %w", err)
		}
	}

	return n, nil
}

func (p *Port) Close() error {
	var err error
	p.closeOnce.Do(func() {
		// Close the serial port - this will cause ReadASHFrame() to return an error
		// which will cause the loop to exit when it checks for os.ErrClosed
		err = p.sp.Close()

		// Signal that we're closing
		close(p.done)
	})
	return err
}

// Drain reads and discards any pending data from the serial port.
// Uses ReadRawBytes with a short timeout to avoid blocking.
// Also drains the bufio.Reader buffer if it has buffered data.
// This is critical when we've read data with ReadRawBytes (bypassing bufio.Reader)
// and need to ensure bufio.Reader is in sync.
func (p *Port) Drain() error {
	// First, drain the bufio.Reader buffer completely (non-blocking)
	// Keep reading until buffer is empty
	// NOTE: This is critical - if we don't drain the buffer, we might start reading
	// in the middle of a frame, causing "skipping bytes" messages. We need to ensure
	// the buffer is completely empty before starting to read frames.
	for {
		buffered := p.reader.Buffered()
		if buffered == 0 {
			break
		}
		buf := make([]byte, buffered)
		n, _ := p.reader.Read(buf)
		if n == 0 {
			break
		}
		// Continue until buffer is empty
	}

	// Use ReadRawBytes with a very short timeout to drain without blocking
	// This will read any immediately available data from the underlying connection
	// NOTE: We read and discard this data to ensure the serial port buffer is clear
	_, err := p.ReadRawBytes(10*time.Millisecond, 256)
	if err != nil && err != io.ErrNoProgress {
		return err
	}
	// Try a couple more times to catch any buffered data
	for i := 0; i < 2; i++ {
		buf, err := p.ReadRawBytes(5*time.Millisecond, 256)
		if err != nil && err != io.ErrNoProgress {
			return err
		}
		if len(buf) == 0 {
			break
		}
	}
	return nil
}

// SyncToFrameBoundary ensures we're synchronized to a frame boundary by reading
// until we find a FLAG byte. This prevents starting to read in the middle of a frame.
// Returns true if a FLAG byte was found, false if timeout/error.
// NOTE: This method uses Peek() to check for FLAG bytes without consuming them,
// ensuring that ReadASHFrame() will see the FLAG byte when it starts reading.
func (p *Port) SyncToFrameBoundary(timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)

	// First, check if there's already a FLAG byte in the bufio.Reader buffer
	// by peeking at buffered data (without consuming it)
	for p.reader.Buffered() > 0 {
		peeked, err := p.reader.Peek(1)
		if err != nil {
			return false, err
		}
		if peeked[0] == ASH_FLAG {
			// Found FLAG at start of buffer - we're synchronized
			// Don't consume it - let ReadASHFrame() read it
			return true, nil
		}
		// Not a FLAG, discard this byte (it's garbage/partial frame)
		_, _ = p.reader.ReadByte()
	}

	// Buffer is empty - wait for a FLAG byte to arrive
	// Use ReadByte() which will read through bufio.Reader, ensuring synchronization
	// But we'll peek first to avoid consuming the FLAG
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return false, io.EOF
		default:
		}

		// Check if data arrived in buffer
		if p.reader.Buffered() > 0 {
			peeked, err := p.reader.Peek(1)
			if err == nil && len(peeked) > 0 && peeked[0] == ASH_FLAG {
				// Found FLAG - don't consume it, let ReadASHFrame() read it
				return true, nil
			}
			// Not a FLAG, discard
			_, _ = p.reader.ReadByte()
			continue
		}

		// Buffer empty, wait a bit for data to arrive
		time.Sleep(50 * time.Millisecond)
	}

	return false, io.ErrNoProgress
}

// ReadASHFrameWithTimeout reads a complete ASH frame with a timeout.
func (p *Port) ReadASHFrameWithTimeout(timeout time.Duration) ([]byte, error) {
	type result struct {
		frame []byte
		err   error
	}

	resultCh := make(chan result, 1)

	go func() {
		frame, err := p.ReadASHFrame()
		resultCh <- result{frame, err}
	}()

	select {
	case res := <-resultCh:
		return res.frame, res.err
	case <-time.After(timeout):
		return nil, io.ErrNoProgress
	}
}

// ReadASHFrame reads a complete ASH frame from the serial port.
// WARNING: This function will block indefinitely if no flag byte is received.
// Use ReadASHFrameWithTimeout for timeouts.
// This function respects the port's done channel for cancellation.
func (p *Port) ReadASHFrame() ([]byte, error) {
	var frame []byte

	// Check if we're closing
	select {
	case <-p.done:
		return nil, io.EOF
	default:
	}

	// Read until we find a flag byte (start of frame)
	// If we read non-FLAG bytes, they might be part of a frame that started
	// before we began reading (the leading FLAG was already consumed)
	// NOTE: This "skipping" is expected behavior - it means we started reading
	// in the middle of a frame. We reconstruct the frame by adding FLAG bytes.
	// However, if this happens frequently, it may indicate:
	// 1. Buffer not properly drained before starting to read
	// 2. Starting to read before device is ready
	// 3. Device sending data faster than we can process
	skippedBytes := 0
	var potentialFrameData []byte

	for {
		// Check if we're closing before each read
		select {
		case <-p.done:
			return nil, io.EOF
		default:
		}

		b, err := p.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == ASH_FLAG {
			if skippedBytes > 0 {
				// We read some bytes before finding a FLAG
				// These might be frame data from a frame that started earlier
				// Try to parse it as a frame by adding a leading FLAG
				potentialFrame := make([]byte, 0, len(potentialFrameData)+2)
				potentialFrame = append(potentialFrame, ASH_FLAG) // Add leading FLAG
				potentialFrame = append(potentialFrame, potentialFrameData...)
				potentialFrame = append(potentialFrame, ASH_FLAG) // Add trailing FLAG

				// Try to parse it
				_, parseErr := ParseASHFrame(potentialFrame)
				if parseErr == nil {
					// Successfully reconstructed frame - this is normal when starting mid-frame
					// Only log if we're debugging, as this is expected behavior
					if false { // Disable verbose logging for normal operation
						fmt.Printf("[ember] ReadASHFrame: reconstructed frame from %d skipped bytes: %X\n", skippedBytes, potentialFrame)
						os.Stdout.Sync()
					}
					return potentialFrame, nil
				}

				// Not a valid frame - discard the garbage bytes and start fresh from this FLAG
				// This happens when we start reading in the middle of corrupted/invalid data
				// Just log a warning and continue with the FLAG we found
				if skippedBytes > 3 {
					fmt.Printf("[ember] ReadASHFrame: discarded %d non-FLAG bytes before finding FLAG (could not reconstruct: %v)\n", skippedBytes, parseErr)
					os.Stdout.Sync()
				}
				// Reset and start fresh from this FLAG
				skippedBytes = 0
				potentialFrameData = nil
			}
			frame = append(frame, b)
			break
		}
		// Not a FLAG byte - might be frame data
		potentialFrameData = append(potentialFrameData, b)
		skippedBytes++
		// Only log if we skip many bytes (indicates a real problem)
		// Small amounts of skipped bytes (1-3) are normal when starting mid-frame
		// and the frame will be reconstructed successfully
		if skippedBytes > 10 {
			fmt.Printf("[ember] ReadASHFrame: WARNING - skipped %d non-FLAG bytes before finding FLAG (may indicate sync issue)\n", skippedBytes)
			os.Stdout.Sync()
		}
	}

	// Read frame data until we find the end flag
	for {
		// Check if we're closing before each read
		select {
		case <-p.done:
			return nil, io.EOF
		default:
		}

		b, err := p.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		frame = append(frame, b)
		if b == ASH_FLAG {
			break
		}
	}

	return frame, nil
}

// ReadRawBytes reads raw bytes from the serial port with a timeout.
func (p *Port) ReadRawBytes(timeout time.Duration, maxBytes int) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}

	resultCh := make(chan result, 1)

	go func() {
		buf := make([]byte, maxBytes)
		n, err := p.sp.Read(buf)
		if n > 0 {
			resultCh <- result{buf[:n], err}
		} else {
			resultCh <- result{nil, err}
		}
	}()

	select {
	case res := <-resultCh:
		return res.data, res.err
	case <-time.After(timeout):
		return nil, io.ErrNoProgress
	}
}

// NextSequence returns the next sequence number for EZSP frames.
func (p *Port) NextSequence() uint8 {
	return uint8(atomic.AddUint32(&p.sequence, 1))
}

// Buffered returns the number of bytes currently buffered in the reader.
func (p *Port) Buffered() int {
	return p.reader.Buffered()
}

// BufferedReadByte reads a single byte from the buffered reader.
// This is used by the continuous byte reader goroutine.
func (p *Port) BufferedReadByte() (byte, error) {
	return p.reader.ReadByte()
}

// CheckBootloaderMode attempts to detect if the device is in bootloader mode.
func (p *Port) CheckBootloaderMode() (bool, []byte, error) {
	rawBytes, err := p.ReadRawBytes(3*time.Second, 512)
	if err != nil && err != io.ErrNoProgress {
		return false, nil, err
	}

	if len(rawBytes) == 0 {
		return false, nil, nil
	}

	text := string(rawBytes)
	lowerText := strings.ToLower(text)
	bootloaderKeywords := []string{
		"bootloader", "gecko", "version", "silicon labs", "ember", "ezsp", "ncp",
		"ets ", "rst:0x", "boot:0x", "spi_fast_flash", "poweron_reset",
	}

	for _, keyword := range bootloaderKeywords {
		if strings.Contains(lowerText, keyword) {
			return true, rawBytes, nil
		}
	}

	if strings.Contains(text, ">") || strings.Contains(text, "#") || strings.Contains(text, "$") {
		return true, rawBytes, nil
	}

	allPrintable := true
	for _, b := range rawBytes {
		if b < 0x20 || b > 0x7E {
			if b != 0x0A && b != 0x0D && b != 0x09 {
				allPrintable = false
				break
			}
		}
	}
	if allPrintable && len(rawBytes) > 10 {
		return true, rawBytes, nil
	}

	return false, rawBytes, nil
}

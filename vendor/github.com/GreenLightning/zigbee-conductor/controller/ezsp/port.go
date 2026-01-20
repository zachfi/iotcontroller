package ezsp

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/jacobsa/go-serial/serial"
	"golang.org/x/sys/unix"
)

type Port struct {
	sp       io.ReadWriteCloser
	reader   *bufio.Reader
	sequence uint32 // Sequence number for EZSP frames
	fd       *os.File // File descriptor for ioctl operations (Linux only)
}

func NewPort(name string) (*Port, error) {
	options := serial.OpenOptions{
		PortName:          name,
		BaudRate:          115200,
		DataBits:          8,
		StopBits:          1,
		ParityMode:        serial.PARITY_NONE, // No parity (8N1)
		RTSCTSFlowControl: false,             // Disable hardware flow control (rtscts: false)
		MinimumReadSize:   1,
	}

	sp, err := serial.Open(options)
	if err != nil {
		return nil, err
	}

	port := &Port{
		sp:     sp,
		reader: bufio.NewReaderSize(sp, 256),
	}

	// Try to get file descriptor for ioctl operations (Linux)
	// The serial library returns *os.File on Linux, which we can use for ioctl
	if file, ok := sp.(*os.File); ok {
		port.fd = file
		log.Printf("[ezsp] Got file descriptor for DTR/RTS control\n")
	} else {
		log.Printf("[ezsp] Serial port is not *os.File, ioctl DTR/RTS control may not be available\n")
	}

	return port, nil
}

// ResetDevice attempts to reset the EZSP device by toggling DTR line.
// According to AN706, nRESET (hardware reset) may be needed for proper initialization.
// For USB devices, DTR is typically used to control the reset line.
// This implements the nRESET sequence: pull low, wait, release high.
func (p *Port) ResetDevice() error {
	// First try the serial library's interface if available
	if dtrControl, ok := p.sp.(interface{ SetDTR(bool) error }); ok {
		log.Printf("[ezsp] Performing hardware reset via DTR (nRESET)...\n")
		if err := dtrControl.SetDTR(false); err != nil {
			return fmt.Errorf("failed to assert DTR (reset): %w", err)
		}
		time.Sleep(10 * time.Millisecond)
		if err := dtrControl.SetDTR(true); err != nil {
			return fmt.Errorf("failed to release DTR (reset): %w", err)
		}
		time.Sleep(50 * time.Millisecond)
		log.Printf("[ezsp] Hardware reset complete\n")
		return nil
	}
	
	// Try RTS as fallback through library interface
	if rtsControl, ok := p.sp.(interface{ SetRTS(bool) error }); ok {
		log.Printf("[ezsp] DTR not available, trying RTS for reset...\n")
		rtsControl.SetRTS(false)
		time.Sleep(10 * time.Millisecond)
		rtsControl.SetRTS(true)
		time.Sleep(50 * time.Millisecond)
		log.Printf("[ezsp] Reset via RTS complete\n")
		return nil
	}
	
	// If library doesn't support it, try direct ioctl on Linux
	if p.fd != nil {
		return p.resetViaIoctl()
	}
	
	// If all else fails, log a warning
	log.Printf("[ezsp] WARNING: Hardware reset (nRESET) not available - serial library doesn't support DTR/RTS control\n")
	log.Printf("[ezsp] Device may need manual reset or may not initialize properly\n")
	return nil
}

// resetViaIoctl performs hardware reset using Linux ioctl to control DTR line.
// This is needed when the serial library doesn't expose DTR/RTS control.
func (p *Port) resetViaIoctl() error {
	log.Printf("[ezsp] Attempting hardware reset via ioctl (DTR control)...\n")
	
	// Get current modem status
	var status uint
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMGET, uintptr(unsafe.Pointer(&status)))
	if errno != 0 {
		log.Printf("[ezsp] WARNING: Could not get modem status via ioctl: %v\n", errno)
		return nil // Not fatal, continue without reset
	}
	
	// Assert reset: Clear DTR bit (pull DTR low)
	status &^= unix.TIOCM_DTR
	_, _, errno = unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMSET, uintptr(unsafe.Pointer(&status)))
	if errno != 0 {
		log.Printf("[ezsp] WARNING: Could not set DTR low via ioctl: %v\n", errno)
		return nil
	}
	
	// Hold reset for at least 1ms (AN706 recommends 1-10ms)
	time.Sleep(10 * time.Millisecond)
	
	// Release reset: Set DTR bit (pull DTR high)
	status |= unix.TIOCM_DTR
	_, _, errno = unix.Syscall(unix.SYS_IOCTL, p.fd.Fd(), unix.TIOCMSET, uintptr(unsafe.Pointer(&status)))
	if errno != 0 {
		log.Printf("[ezsp] WARNING: Could not set DTR high via ioctl: %v\n", errno)
		return nil
	}
	
	// Wait for device to stabilize after reset
	time.Sleep(50 * time.Millisecond)
	log.Printf("[ezsp] Hardware reset via ioctl complete\n")
	return nil
}

func (p *Port) ReadBytes(buf []byte) (int, error) {
	return p.reader.Read(buf)
}

func (p *Port) WriteBytes(buf []byte) (int, error) {
	return p.sp.Write(buf)
}

func (p *Port) Close() error {
	return p.sp.Close()
}

// Drain reads and discards any pending data from the serial port.
// This is useful before starting a handshake to clear any old data.
// It attempts to read for a short time and then gives up to avoid blocking.
func (p *Port) Drain() error {
	// Try to read any pending data, but don't block forever
	buf := make([]byte, 256)
	// Use a small buffer check - if ReadByte would block, we're done
	// We can't easily check if data is available without blocking in Go's serial library,
	// so we'll just try a few quick reads
	for i := 0; i < 10; i++ {
		// Try to peek/read - if it would block, that's fine
		// We use a small timeout approach: try reading, if it blocks immediately, stop
		n, _ := p.reader.Read(buf)
		if n == 0 {
			break
		}
	}
	return nil
}

// ReadASHFrameWithTimeout reads a complete ASH frame with a timeout.
// It reads from the first flag byte (0x7E) until the next flag byte.
// Returns an error if no flag byte is found within the timeout period.
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
		return nil, io.ErrNoProgress // Use a standard error to indicate timeout
	}
}

// ReadASHFrame reads a complete ASH frame from the serial port.
// It reads from the first flag byte (0x7E) until the next flag byte.
// The frame includes the flag delimiters. Escaped bytes are read as-is
// and will be unescaped by ParseASHFrame.
// WARNING: This function will block indefinitely if no flag byte is received.
// Use ReadASHFrameWithTimeout for timeouts.
func (p *Port) ReadASHFrame() ([]byte, error) {
	var frame []byte
	
	// Read until we find a flag byte (start of frame)
	// Skip any garbage bytes before the first flag
	for {
		b, err := p.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == ASH_FLAG {
			frame = append(frame, b)
			break
		}
		// Skip non-flag bytes (garbage/old data that might be in buffer)
	}
	
	// Read frame data until we find the end flag
	// Note: If 0x7E appears in data, it's escaped as 0x7D 0x5E, so we won't
	// see a raw 0x7E in the middle of data. The unescape function handles this.
	for {
		b, err := p.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		frame = append(frame, b)
		if b == ASH_FLAG {
			// End of frame
			break
		}
	}
	
	return frame, nil
}

// ReadRawBytes reads raw bytes from the serial port with a timeout.
// Useful for debugging to see what's actually being received.
// This reads directly from the underlying serial port, bypassing bufio buffering.
func (p *Port) ReadRawBytes(timeout time.Duration, maxBytes int) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	
	resultCh := make(chan result, 1)
	
	go func() {
		// Read directly from serial port to avoid bufio buffering issues
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

// CheckBootloaderMode attempts to detect if the device is in bootloader mode.
// Bootloader mode typically sends ASCII text or different frame patterns.
// Returns true if bootloader mode is detected, false otherwise.
// Also detects ESP32 boot messages (Sonoff Dongle Max uses ESP32 as bridge).
func (p *Port) CheckBootloaderMode() (bool, []byte, error) {
	// Try to read any data the device might be sending
	// Bootloader mode often sends ASCII text like "Gecko Bootloader vX.X.X"
	// ESP32 bootloader sends messages like "ets Jul 29 2019..."
	rawBytes, err := p.ReadRawBytes(3*time.Second, 512) // Increased timeout and buffer for boot messages
	if err != nil && err != io.ErrNoProgress {
		return false, nil, err
	}
	
	if len(rawBytes) == 0 {
		return false, nil, nil // No data, can't determine
	}
	
	// Check for common bootloader indicators:
	// 1. ASCII text containing "bootloader", "gecko", "version", etc.
	text := string(rawBytes)
	lowerText := strings.ToLower(text)
	bootloaderKeywords := []string{
		"bootloader",
		"gecko",
		"version",
		"silicon labs",
		"ember",
		"ezsp",
		"ncp",
		"ets ",           // ESP32 bootloader marker
		"rst:0x",         // ESP32 reset reason
		"boot:0x",        // ESP32 boot mode
		"spi_fast_flash", // ESP32 flash boot
		"poweron_reset",  // ESP32 power-on reset
	}
	
	for _, keyword := range bootloaderKeywords {
		if strings.Contains(lowerText, keyword) {
			return true, rawBytes, nil
		}
	}
	
	// 2. Check for common bootloader prompt patterns
	if strings.Contains(text, ">") || strings.Contains(text, "#") || strings.Contains(text, "$") {
		return true, rawBytes, nil
	}
	
	// 3. Check if data looks like ASCII (printable characters)
	// Bootloader often sends ASCII, while ASH uses binary
	allPrintable := true
	for _, b := range rawBytes {
		if b < 0x20 || b > 0x7E {
			if b != 0x0A && b != 0x0D && b != 0x09 { // Allow LF, CR, TAB
				allPrintable = false
				break
			}
		}
	}
	if allPrintable && len(rawBytes) > 10 {
		// Likely bootloader sending ASCII text
		return true, rawBytes, nil
	}
	
	return false, rawBytes, nil
}

// WaitForBootCompletion waits for the device to finish booting.
// For ESP32-based devices, this waits for boot messages to complete.
// Returns any remaining boot data and an error if timeout occurs.
func (p *Port) WaitForBootCompletion(timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var allBootData []byte
	
	log.Printf("[ezsp] Waiting for device to finish booting (max %v)...\n", timeout)
	
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining < 100*time.Millisecond {
			remaining = 100 * time.Millisecond
		}
		
		data, err := p.ReadRawBytes(remaining, 512)
		if err == nil && len(data) > 0 {
			allBootData = append(allBootData, data...)
			// Check if boot is complete - no more boot messages
			// ESP32 typically stops sending after a few seconds
			// If we get data, continue reading for a bit more
			time.Sleep(200 * time.Millisecond)
			continue
		} else if err == io.ErrNoProgress {
			// No more data - boot might be complete
			if len(allBootData) > 0 {
				log.Printf("[ezsp] Boot messages completed, %d bytes received\n", len(allBootData))
				return allBootData, nil
			}
			// No data at all - might already be booted or still booting
			time.Sleep(100 * time.Millisecond)
			continue
		} else if err != nil {
			return allBootData, err
		}
	}
	
	if len(allBootData) > 0 {
		log.Printf("[ezsp] Boot timeout, but received %d bytes of boot data\n", len(allBootData))
		return allBootData, nil
	}
	
	return nil, fmt.Errorf("timeout waiting for boot completion")
}

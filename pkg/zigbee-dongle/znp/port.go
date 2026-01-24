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
	sp  io.ReadWriteCloser
	cbs Callbacks

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
		sp:  sp,
		cbs: callbacks,

		handlerMutex: sync.Mutex{},
		handlers:     make(map[FrameHeader]*Handler),
		done:         make(chan struct{}),
	}

	// Try to get file descriptor for ioctl operations (Linux)
	// The serial library returns *os.File on Linux, which we can use for ioctl
	if file, ok := sp.(*os.File); ok {
		port.fd = file
	}

	go port.loop()

	return port, nil
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

	if frame.Type == FRAME_TYPE_SREQ {
		responseHeader := frame.FrameHeader
		responseHeader.Type = FRAME_TYPE_SRSP
		handler = p.registerHandler(responseHeader, timeout)
	}

	err := writeFrame(p.sp, frame)
	if err != nil {
		return nil, err
	}

	if handler != nil {
		return handler.Receive()
	}

	return nil, nil
}

func (p *Port) loop() {
	r := bufio.NewReaderSize(p.sp, 256)
	for {
		// Check if we should stop before attempting to read
		select {
		case <-p.done:
			return
		default:
		}

		frame, err := readFrame(r)
		if err == io.ErrNoProgress {
			continue
		}
		if err != nil {
			// Check if we're closing
			select {
			case <-p.done:
				return
			default:
			}

			if errors.Is(err, os.ErrClosed) || err == io.EOF {
				break
			}
			var handling ErrorHandling
			if p.cbs.OnReadError != nil {
				handling = p.cbs.OnReadError(err)
			}
			if handling == ErrorHandlingContinue {
				continue
			} else if handling == ErrorHandlingStop {
				break
			} else {
				panic(err)
			}
		}

		command, err := parseCommandFromFrame(frame)
		if err != nil {
			handling := ErrorHandlingStop
			if p.cbs.OnParseError != nil {
				handling = p.cbs.OnParseError(err, frame)
			}
			if handling == ErrorHandlingContinue {
				continue
			} else if handling == ErrorHandlingStop {
				break
			} else {
				panic(err)
			}
		}

		if p.cbs.AfterRead != nil {
			p.cbs.AfterRead(command)
		}

		p.handlerMutex.Lock()
		handler := p.handlers[frame.FrameHeader]
		if handler != nil && handler.timer != nil {
			delete(p.handlers, frame.FrameHeader)
		}
		p.handlerMutex.Unlock()

		if handler != nil {
			handler.fulfill(command)
		}
	}
}

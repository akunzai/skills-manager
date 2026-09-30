package tui

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var readConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")

// consoleKeyRecord has INPUT_RECORD's DWORD-aligned union layout. Other
// record types share this storage and are skipped before its key fields
// are inspected. https://learn.microsoft.com/windows/console/input-record-str
// https://learn.microsoft.com/windows/console/key-event-record-str
// Windows Terminal translates VT input (including SGR mouse and CPR) into
// key-down UnicodeChar records before storing them in the console queue.
type consoleKeyRecord struct {
	eventType                      uint16
	_                              uint16
	down                           int32
	repeat, virtualKey, scan, char uint16
	control                        uint32
}

func readInput(timeout time.Duration) ([]byte, error) {
	handle := windows.Handle(os.Stdin.Fd())
	status, err := windows.WaitForSingleObject(handle, uint32(timeout.Milliseconds()))
	if err != nil {
		return nil, err
	}
	if status == uint32(windows.WAIT_TIMEOUT) {
		return nil, nil
	}
	if status != windows.WAIT_OBJECT_0 {
		return nil, fmt.Errorf("unexpected console input wait status: %d", status)
	}
	var available uint32
	if err := windows.GetNumberOfConsoleInputEvents(handle, &available); err != nil {
		return nil, err
	}
	if available == 0 {
		return nil, nil
	}
	var records [64]consoleKeyRecord
	var count uint32
	result, _, err := readConsoleInput.Call(uintptr(handle), uintptr(unsafe.Pointer(&records[0])), uintptr(min(available, uint32(len(records)))), uintptr(unsafe.Pointer(&count)))
	if result == 0 {
		return nil, err
	}
	var input []byte
	for _, record := range records[:count] {
		if record.eventType != 1 || record.down == 0 || record.char == 0 {
			continue
		}
		for range record.repeat {
			input = append(input, string(rune(record.char))...)
		}
	}
	// Reading records instead of ReadFile consumes non-character wakeups
	// without blocking on a filtered focus, key-up or resize event.
	return input, nil
}

func enableVTOutput() (func(), error) {
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return func() {}, nil
	}
	if err := windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return nil, err
	}
	return func() { _ = windows.SetConsoleMode(handle, mode) }, nil
}

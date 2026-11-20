package main

import (
	"bufio"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// Lectura de contraseñas sin eco con ioctl (TCGETS/TCSETS), para no depender
// de golang.org/x/term.

func getTermios(fd uintptr) (syscall.Termios, error) {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	if errno != 0 {
		return t, errno
	}
	return t, nil
}

func setTermios(fd uintptr, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}

func isTerminal(fd uintptr) bool {
	_, err := getTermios(fd)
	return err == nil
}

// readNoEcho lee una línea del terminal con el eco desactivado y lo restaura
// después.
func readNoEcho(f *os.File) (string, error) {
	fd := f.Fd()
	old, err := getTermios(fd)
	if err != nil {
		return "", err
	}
	noEcho := old
	noEcho.Lflag &^= syscall.ECHO
	noEcho.Lflag |= syscall.ICANON | syscall.ISIG
	if err := setTermios(fd, &noEcho); err != nil {
		return "", err
	}
	defer setTermios(fd, &old)
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

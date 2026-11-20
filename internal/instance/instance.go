// Package instance garantiza que solo haya una instancia de qaphi en marcha
// en el sistema, sin ficheros ni rutas fijas.
//
// Se usa un socket Unix del espacio de nombres abstracto de Linux
// ("@qaphi"): no existe en el sistema de ficheros y el núcleo lo libera
// automáticamente cuando el proceso termina, aunque sea por un fallo, así que
// nunca quedan bloqueos huérfanos. El ámbito es el espacio de nombres de red:
// dos contenedores con red propia no se ven entre sí, lo que es lo esperado.
package instance

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"
)

// DefaultName es el nombre del bloqueo de la instancia de qaphi.
const DefaultName = "qaphi"

// ErrAlreadyRunning indica que ya hay otra instancia en marcha.
var ErrAlreadyRunning = errors.New("ya hay una instancia de qaphi en marcha en este sistema")

// Lock es el bloqueo de instancia única. Se libera con Release o al terminar
// el proceso.
type Lock struct {
	ln net.Listener
}

// Acquire toma el bloqueo con el nombre dado. Si ya lo tiene otro proceso,
// devuelve un error que envuelve ErrAlreadyRunning e incluye su PID cuando se
// puede averiguar.
func Acquire(name string) (*Lock, error) {
	addr := "@" + name
	ln, err := net.Listen("unix", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			if pid := holderPID(addr); pid > 0 {
				return nil, fmt.Errorf("%w (PID %d)", ErrAlreadyRunning, pid)
			}
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("bloqueo de instancia única: %w", err)
	}
	l := &Lock{ln: ln}
	// Se aceptan y cierran las conexiones entrantes (las de otra instancia
	// que pregunta quién tiene el bloqueo), para no llenar la cola.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return l, nil
}

// Release libera el bloqueo.
func (l *Lock) Release() error {
	return l.ln.Close()
}

// holderPID obtiene el PID del proceso que tiene el bloqueo, mediante las
// credenciales del extremo remoto (SO_PEERCRED). Devuelve 0 si no se puede.
func holderPID(addr string) int32 {
	c, err := net.DialTimeout("unix", addr, time.Second)
	if err != nil {
		return 0
	}
	defer c.Close()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	var pid int32
	_ = raw.Control(func(fd uintptr) {
		if cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED); err == nil {
			pid = cred.Pid
		}
	})
	return pid
}

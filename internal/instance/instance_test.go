package instance

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func testName(t *testing.T) string {
	return fmt.Sprintf("qaphi-test-%d-%d", os.Getpid(), time.Now().UnixNano())
}

func TestAcquire(t *testing.T) {
	name := testName(t)
	l, err := Acquire(name)
	if err != nil {
		t.Fatalf("primer Acquire: %v", err)
	}
	_, err = Acquire(name)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("segundo Acquire: error = %v, se esperaba ErrAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("PID %d", os.Getpid())) {
		t.Errorf("el error debería indicar el PID del titular: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := Acquire(name)
	if err != nil {
		t.Fatalf("Acquire tras liberar: %v", err)
	}
	l2.Release()
}

func TestNombresIndependientes(t *testing.T) {
	a, err := Acquire(testName(t) + "-a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := Acquire(testName(t) + "-b")
	if err != nil {
		t.Fatalf("un nombre distinto no debe bloquearse: %v", err)
	}
	b.Release()
}

//go:build !linux

package main

// Qaphi solo se desarrolla y se distribuye para Linux. Este fichero hace que
// la compilación falle con un mensaje explícito en cualquier otro sistema.
var _ = qaphi_solo_se_compila_para_linux

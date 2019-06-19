package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/tarm/serial"
)

func stdin(tx chan byte) {
	buf := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(buf)
		if err != nil {
			log.Fatal(err)
		}
		tx <- buf[0]
	}
}

func stdout(rx chan byte) {
	buf := make([]byte, 1)
	for {
		buf[0] = <-rx
		os.Stdout.Write(buf)
	}
}

func serrx(port *serial.Port, tx chan byte) {
	buf := make([]byte, 1)
	for {
		_, err := port.Read(buf)
		if err != nil {
			log.Fatal(err)
		}
		tx <- buf[0]
	}
}

func sertx(port *serial.Port, rx chan byte) {
	buf := make([]byte, 1)
	for {
		buf[0] = <-rx
		_, err := port.Write(buf)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func main() {
	var (
		baud = flag.Int("b", 115200, "baudrate")
	)
	flag.Parse()
	args := flag.Args()
	portName := args[0]
	fmt.Printf("%s, baud:%d\n", portName, *baud)
	port, err := serial.OpenPort(&serial.Config{Name: portName, Baud: *baud})
	if err != nil {
		log.Fatal(err)
	}
	defer port.Close()

	toStdout := make(chan byte)
	toSerTx := make(chan byte)
	fromStdin := make(chan byte)
	fromSerRx := make(chan byte)

	go sertx(port, toSerTx)
	go serrx(port, fromSerRx)
	go stdout(toStdout)
	go stdin(fromStdin)

	for {
		select {
		case x := <-fromSerRx:
			toStdout <- x
		case x := <-fromStdin:
			toSerTx <- x
		}
	}
}

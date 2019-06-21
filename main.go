package main

import (
	"container/list"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"github.com/tarm/serial"
)

func read(reader io.Reader, tx chan byte) {
	buf := make([]byte, 1)
	for {
		_, err := reader.Read(buf)
		if err != nil {
			log.Fatal(err)
		}
		tx <- buf[0]
	}
}

func write(writer io.Writer, rx chan byte) {
	buf := make([]byte, 1)
	for {
		buf[0] = <-rx
		writer.Write(buf)
	}
}

func stdin(tx chan byte) {
	read(os.Stdin, tx)
}

func stdout(rx chan byte) {
	write(os.Stdout, rx)
}

func serrx(port *serial.Port, tx chan byte) {
	read(port, tx)
}

func sertx(port *serial.Port, rx chan byte) {
	write(port, rx)
}

func tcpfw(conn net.Conn, rx chan byte, tx chan byte) {
	go read(conn, tx)
	write(conn, rx)
}

func udpdw(conn net.UDPConn, rx chan byte) {
	write(&conn, rx)
}

func duplicator(rx chan byte, register chan (chan byte), remove chan (chan byte)) {
	cs := list.New()
	m := make(map[chan byte]*list.Element)
	for {
		select {
		case x := <-rx:
			for e := cs.Front(); e != nil; e = e.Next() {
				e.Value.(chan byte) <- x
			}
		case x := <-register:
			e := cs.PushBack(x)
			m[x] = e
			log.Println("register num", len(m), cs.Len())
		case x := <-remove:
			cs.Remove(m[x])
			delete(m, x)
			log.Println("remove num", len(m), cs.Len())
		}
	}
}
func handleConnection(conn *net.TCPConn, rx chan byte, tx chan byte, remove chan (chan byte)) {
	defer conn.Close()
	defer func() { remove <- rx }()
	go write(conn, rx)
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok {
				switch {
				case ne.Temporary():
					continue
				}
			}
			log.Println("Read", err)
			return
		}
		for _, x := range buf[:n] {
			tx <- x
		}
	}

}

func serveTCP(tcp string, rx chan byte, tx chan byte) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", tcp)
	log.Println("ResolveTCPAddr => tcpAddr", tcpAddr)
	if err != nil {
		log.Fatal("ResolveTCPAddr", err)
	}
	l, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		log.Fatal("ListenTCP", err)
	}
	log.Println("start Listen")
	defer l.Close()
	register := make(chan (chan byte))
	remove := make(chan (chan byte))
	go duplicator(rx, register, remove)
	for {
		conn, err := l.AcceptTCP()
		if err != nil {
			log.Fatal("Accept", err)
		}
		log.Println("Accepcted", conn.RemoteAddr())
		rx2 := make(chan byte)
		go handleConnection(conn, rx2, tx, remove)
		register <- rx2
	}
}

func main() {
	var (
		baud = flag.Int("b", 115200, "baudrate")
		tcp  = flag.String("tcp", "", "tcp foward destination")
	)
	flag.Parse()
	args := flag.Args()
	portName := args[0]
	fmt.Printf("%s, baud:%d\n", portName, *baud)
	port, err := serial.OpenPort(&serial.Config{Name: portName, Baud: *baud})
	if err != nil {
		log.Fatal("serial.OpenPort", err)
	}
	defer port.Close()

	toStdout := make(chan byte)
	toSerTx := make(chan byte)
	fromStdin := make(chan byte)
	fromSerRx := make(chan byte)

	var chans []chan byte
	chans = append(chans, toStdout)
	if *tcp != "" {
		toTCP := make(chan byte)
		go serveTCP(*tcp, toTCP, toSerTx)
		chans = append(chans, toTCP)
	}

	go sertx(port, toSerTx)
	go serrx(port, fromSerRx)
	go stdout(toStdout)
	go stdin(fromStdin)

	for {
		select {
		case x := <-fromSerRx:
			for _, c := range chans {
				c <- x
			}
		case x := <-fromStdin:
			toSerTx <- x
		}
	}
}

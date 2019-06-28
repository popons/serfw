package main

import (
	"container/list"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/armon/circbuf"

	"github.com/tarm/serial"
)

func read(reader io.Reader, tx chan byte) {
	buf := make([]byte, 1)
	for {
		_, err := reader.Read(buf)
		if err != nil {
			log.Println(err)
			break
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

// exists returns whether the given file or directory exists
func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}
func appendLogFile(msg string) {
	if yes, _ := exists(*logDir); !yes {
		err := os.MkdirAll(*logDir, 0777)
		if err != nil {
			log.Fatal("os.MkdirAll", err)
		}
	}
	logPath := fmt.Sprintf("%s.txt", time.Now().Format("2006-01-02"))
	logFullPath := path.Join(*logDir, logPath)
	file, err := os.OpenFile(logFullPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		//エラー処理
		log.Fatal(err)
	}
	defer file.Close()
	fmt.Fprintln(file, msg)
}

func toValidUtf8Lossy(src []byte) []byte {
	for {
		if utf8.Valid(src) {
			return src
		} else {
			src = src[:len(src)-1]
		}
	}
}

func tcpfw(conn net.Conn, rx chan byte, tx chan byte) {
	const layout2 = "[2006-01-02 15:04:05.000] "
	go read(conn, tx)
	lineBuf, _ := circbuf.NewBuffer(4096)

	var timeStamp time.Time
	for {
		x := <-rx
		if lineBuf.TotalWritten() == 0 {
			timeStamp = time.Now()
		}
		if x != 0xA && x != 0xD {
			lineBuf.Write([]byte{x})
		}
		if x == 0xA {
			bytes := lineBuf.Bytes()
			bytes = toValidUtf8Lossy(bytes)
			lineBuf.Reset()
			if len(bytes) != 0 {
				msg := timeStamp.Format(layout2) + strings.TrimRight(string(bytes), " \t\r\n")
				appendLogFile(msg)
				_, err := conn.Write([]byte(msg + "\n"))
				if err != nil {
					log.Println("conn.Write()", err)
					break
				}
			}
		}
	}
}

func udpdw(conn net.UDPConn, rx chan byte) {
	write(&conn, rx)
}

func duplicator(rx <-chan byte, register chan (chan byte), remove chan (chan byte)) {
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

func serveTCP(tcps string, rx chan byte, tx chan byte) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", tcps)
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
func startTCP(tcp string, toSerTx chan byte) chan byte {
	toTCP := make(chan byte)
	n := 1
	go func() {
		for {
			conn, err := net.Dial("tcp", tcp)
			if err != nil {
				log.Fatal(err)
			}
			defer conn.Close()
			tcpfw(conn, toTCP, toSerTx)
			log.Println("tcpfw() exiit , wait 5sec next try")
			time.Sleep(5 * time.Second)
			n++
			log.Printf("tcp connection %d times\n", n)
		}
	}()
	return toTCP
}

var (
	logDir = flag.String("logdir", "sup-log", "used as log file save directory")
)

func sendStringToChan(x string, tx chan<- byte) {
	for _, x := range []byte(x) {
		tx <- x
	}
}

func main() {
	argss := os.Args
	fmt.Println(argss)
	var (
		baud   = flag.Int("b", 115200, "baudrate")
		tcps   = flag.String("tcps", "", "tcps foward destination")
		tcpc   = flag.String("tcpc", "", "tcpc foward destination")
		pktLoc = flag.String("ploc", "", "udp packet receive interface")
		pktRem = flag.String("prem", "", "udp packet send destination")
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

	if *tcps != "" {
		toTCP := make(chan byte)
		go serveTCP(*tcps, toTCP, toSerTx)
		chans = append(chans, toTCP)
	}

	if *tcpc != "" {
		toTCP := startTCP(*tcpc, toSerTx)
		chans = append(chans, toTCP)
	}

	if *pktLoc != "" && *pktRem != "" {
		// receive to
		go func() {
			addr, err := net.ResolveUDPAddr("udp", *pktLoc)
			if err != nil {
				log.Fatal("net.ResolveUDPAddr()", err)
			}
			conn, err := net.ListenUDP("udp", addr)
			if err != nil {
				log.Fatal("net.ListenUDP()", addr, err)
			}
			buf := make([]byte, 4096)
			for {
				n, err := conn.Read(buf)
				if err != nil {
					log.Fatal("conn.Read()", err)
				}
				sendStringToChan("pktin\r\n", toSerTx)
				pkts := strings.ReplaceAll(hex.EncodeToString(buf[:n]), " ", "")
				sendStringToChan(pkts, toSerTx)
				sendStringToChan("$\r\n", toSerTx)
			}
		}()
		go func() {
			rx := make(chan byte)
			chans = append(chans, rx)
			lineBuf, _ := circbuf.NewBuffer(4096)
			inPkt := false
			sbuf := make([]byte, 4096)
			conn, err := net.Dial("udp", *pktRem)
			if err != nil {
				log.Fatal("udp.Dial", *pktRem, err)
			}
			for {
				x := <-rx
				if x != 0xA && x != 0xD {
					lineBuf.Write([]byte{x})
				}
				if x == 0xA {
					str := string(lineBuf.Bytes())
					lineBuf.Reset()
					if strings.HasPrefix(str, "#!begin pktout") {
						inPkt = true
					} else if strings.HasPrefix(str, "#!end pktout") {
						inPkt = false
					} else if inPkt && strings.HasSuffix(str, "$") {
						n := len(str) - 1

						for i := 0; i < n; i += 2 {
							x, err := strconv.ParseUint(str[i:i+2], 16, 8)
							if err != nil {
								log.Println("parseint", str[0:n-1], err)
							}
							sbuf[i>>1] = byte(x)
						}
						conn.Write(sbuf[:n>>1])
						tmp := sbuf[:n>>1]
						fmt.Println(hex.Dump(tmp))
					}
				}
			}
		}()
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

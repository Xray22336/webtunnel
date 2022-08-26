package main

import (
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"

	"sync"
	"syscall"

	pt "git.torproject.org/pluggable-transports/goptlib.git"
)

func main() {
	ptInfo, err := pt.ClientSetup(nil)
	if err != nil {
		log.Fatal(err)
	}
	if ptInfo.ProxyURL != nil {
		pt.ProxyError("proxy is not supported")
		os.Exit(1)
	}
	listeners := make([]net.Listener, 0)
	shutdown := make(chan struct{})
	var wg sync.WaitGroup
	for _, methodName := range ptInfo.MethodNames {
		switch methodName {
		case "webtunnel":
			// TODO: Be able to recover when SOCKS dies.
			ln, err := pt.ListenSocks("tcp", "127.0.0.1:0")
			if err != nil {
				pt.CmethodError(methodName, err.Error())
				break
			}
			log.Printf("Started SOCKS listener at %v.", ln.Addr())
			go socksAcceptLoop(ln, nil, shutdown, &wg)
			pt.Cmethod(methodName, ln.Version(), ln.Addr())
			listeners = append(listeners, ln)
		default:
			pt.CmethodError(methodName, "no such method")
		}
	}
	pt.CmethodsDone()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)

	if os.Getenv("TOR_PT_EXIT_ON_STDIN_CLOSE") == "1" {
		// This environment variable means we should treat EOF on stdin
		// just like SIGTERM: https://bugs.torproject.org/15435.
		go func() {
			if _, err := io.Copy(ioutil.Discard, os.Stdin); err != nil {
				log.Printf("calling io.Copy(ioutil.Discard, os.Stdin) returned error: %v", err)
			}
			log.Printf("synthesizing SIGTERM because of stdin close")
			sigChan <- syscall.SIGTERM
		}()
	}

	// Wait for a signal.
	<-sigChan
	log.Println("stopping webtunnel")

	// Signal received, shut down.
	for _, ln := range listeners {
		ln.Close()
	}
	close(shutdown)
	wg.Wait()
	log.Println("webtunnel is done.")

}

// Accept local SOCKS connections and connect to a transport connection
func socksAcceptLoop(ln *pt.SocksListener, _ *struct{}, shutdown chan struct{}, wg *sync.WaitGroup) {
	defer ln.Close()
	for {
		conn, err := ln.AcceptSocks()
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				continue
			}
			log.Printf("SOCKS accept error: %s", err)
			break
		}
		log.Printf("SOCKS accepted: %v", conn.Req)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()

			handler := make(chan struct{})
			go func() {
				defer close(handler)
				var config ClientConfig

				if remoteAddress, ok := conn.Req.Args.Get("addr"); ok {
					config.RemoteAddress = remoteAddress
				}

				if path, ok := conn.Req.Args.Get("path"); ok {
					config.Path = path
				}

				if tlsKind, ok := conn.Req.Args.Get("tls"); ok {
					config.TLSKind = tlsKind
				}

				if tlsServerName, ok := conn.Req.Args.Get("servername"); ok {
					config.TLSServerName = tlsServerName
				}

				transport, err := NewWebTunnelClientTransport(&config)
				if err != nil {
					log.Printf("transport error: %s", err)
					conn.Reject()
					return
				}

				sconn, err := transport.Dial()
				if err != nil {
					log.Printf("dial error: %s", err)
					conn.Reject()
					return
				}
				conn.Grant(nil)
				defer sconn.Close()
				// copy between the created transport conn and the SOCKS conn
				copyLoop(conn, sconn)
			}()
			select {
			case <-shutdown:
				log.Println("Received shutdown signal")
			case <-handler:
				log.Println("Handler ended")
			}
			return
		}()
	}
}

// Exchanges bytes between two ReadWriters.
// (In this case, between a SOCKS connection and a webtunnel transport conn)
func copyLoop(socks, sfconn io.ReadWriter) {
	done := make(chan struct{}, 2)
	go func() {
		if _, err := io.Copy(socks, sfconn); err != nil {
			log.Printf("copying webtunnel to SOCKS resulted in error: %v", err)
		}
		done <- struct{}{}
	}()
	go func() {
		if _, err := io.Copy(sfconn, socks); err != nil {
			log.Printf("copying SOCKS to webtunnel resulted in error: %v", err)
		}
		done <- struct{}{}
	}()
	<-done
	log.Println("copy loop ended")
}

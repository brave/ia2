package main

import (
	"log"
	"net"

	"github.com/mdlayher/vsock"
)

const (
	// According to AWS docs, the CID of the parent instance is always 3:
	// https://docs.aws.amazon.com/enclaves/latest/user/nitro-enclave-concepts.html
	parentCID = 3
	bindAddr  = "127.0.0.1:1080"
)

// VProxy implements a TCP proxy that translates from AF_INET (to the left) to
// AF_VSOCK (to the right).
type VProxy struct {
	raddr *vsock.Addr
	laddr *net.TCPAddr
}

// Start starts the proxy.  Once the proxy is up and running, it signals its
// readiness over the given channel.
func (p *VProxy) Start(done chan bool) {
	// Bind to TCP address.
	ln, err := net.Listen("tcp", bindAddr)
	if err != nil {
		log.Fatalf("Failed to bind to %s: %s", bindAddr, err)
	}
	done <- true // Signal to caller that we're ready to accept connections.

	for {

		log.Printf("Waiting for new outgoing TCP connection.")
		lconn, err := ln.Accept()
		if err != nil {
			log.Printf("Failed to accept proxy connection: %s", err)
			continue
		}
		log.Printf("Accepted new outgoing TCP connection.")

		// Establish connection with SOCKS proxy via our vsock interface.
		rconn, err := vsock.Dial(p.raddr.ContextID, p.raddr.Port)
		if err != nil {
			log.Printf("Failed to establish connection to SOCKS proxy: %s", err)
			continue
		}
		log.Println("Established connection with SOCKS proxy over vsock.")

		// Now pipe data from left to right and vice versa.
		go p.pipe(lconn, rconn)
		go p.pipe(rconn, lconn)
	}
}

// pipe forwards packets from src to dst and from dst to src.
func (p *VProxy) pipe(src, dst net.Conn) {
	defer func() {
		if err := src.Close(); err != nil {
			log.Printf("Failed to close connection: %s", err)
		}
	}()
	buf := make([]byte, 0xffff)
	for {
		n, err := src.Read(buf)
		if err != nil {
			log.Printf("Failed to read from src connection: %s", err)
			return
		}
		b := buf[:n]
		n, err = dst.Write(b)
		if err != nil {
			log.Printf("Failed to write to dst connection: %s", err)
			return
		}
		if n != len(b) {
			log.Printf("Only wrote %d out of %d bytes.", n, len(b))
			return
		}
	}
}

// NewVProxy returns a new vProxy instance.
func NewVProxy() (*VProxy, error) {
	laddr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:1080")
	if err != nil {
		return nil, err
	}

	return &VProxy{
		raddr: &vsock.Addr{ContextID: parentCID, Port: 1080},
		laddr: laddr,
	}, nil
}

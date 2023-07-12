package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	uuid "github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// fastlyClientIP represents the IP address of the client:
	// https://developer.fastly.com/reference/http/http-headers/Fastly-Client-IP/
	// (retrieved on 2021-11-29)
	fastlyClientIP = "Fastly-Client-IP"
	indexPage      = "This request is handled by tokenizer."
)

var (
	errBadApiVersion       = errors.New("invalid ads API version")
	errBadWalletFmt        = errors.New("wallet ID has bad format")
	errNoFastlyHeader      = fmt.Errorf("found no %q header", fastlyClientIP)
	errBadFastlyAddrFormat = fmt.Errorf("bad IP address format in %q header", fastlyClientIP)
)

// clientRequest represents a client's confirmation token request.  It contains
// the client's IP address and wallet ID.
type clientRequest struct {
	Addr   net.IP    `json:"addr"`
	Wallet uuid.UUID `json:"wallet"`
}

func (c *clientRequest) bytes() []byte {
	return c.Addr
}

// webReceiver implements a receiver that exposes an HTTP API to receive data.
type webReceiver struct {
	done   chan empty
	in     chan serializer
	router *chi.Mux
	port   uint16
}

func newWebReceiver() receiver {
	w := &webReceiver{
		in:   make(chan serializer),
		done: make(chan empty),
	}
	w.router = newRouter(w.in)

	return w
}

// isValidApiVersion returns true if we're dealing with ads API version 1, 2,
// 3, or 4.  As of 2023-05-05, version 1 and 2 are outdated, 3 is live, and 4
// is not yet in the works.  For the sake of being future-proof, we do however
// accept version 4 already.
func isValidApiVersion(v string) bool {
	num, err := strconv.ParseUint(v, 10, 0)
	if err != nil {
		return false
	}
	return num >= 1 && num <= 4
}

func newRouter(inbox chan serializer) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/v{version}/confirmation/token/{walletID}", getConfTokenHandler(inbox))
	r.Get("/", indexHandler)
	return r
}

func (w *webReceiver) setConfig(c *config) {
	w.port = c.port
}

func (w *webReceiver) inbox() chan serializer {
	return w.in
}

func (w *webReceiver) start() {
	go func() {
		l.Printf("Starting Web server at :%d.", w.port)
		srv := &http.Server{
			Addr:    fmt.Sprintf(":%d", w.port),
			Handler: w.router,
		}
		l.Fatal(srv.ListenAndServe())
	}()
}

func (w *webReceiver) stop() {
	close(w.done)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, indexPage)
}

func getConfTokenHandler(inbox chan serializer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		errAndReport := func(body string, code int) {
			http.Error(w, body, code)
			m.webResponses.With(prometheus.Labels{
				httpCode: fmt.Sprintf("%d", code),
				httpBody: body,
			}).Inc()
		}

		if !isValidApiVersion(chi.URLParam(r, "version")) {
			errAndReport(errBadApiVersion.Error(), http.StatusBadRequest)
			return
		}
		// Make sure that the wallet ID is a valid UUID.
		rawWalletID := chi.URLParam(r, "walletID")

		walletID, err := uuid.Parse(rawWalletID)
		if err != nil {
			errAndReport(errBadWalletFmt.Error(), http.StatusBadRequest)
			return
		}

		rawAddr := r.Header.Get(fastlyClientIP)
		if rawAddr == "" {
			errAndReport(errNoFastlyHeader.Error(), http.StatusBadRequest)
			return
		}

		// Fetch the client's IP address from Fastly's proprietary header.
		addr := net.ParseIP(rawAddr)
		if addr == nil {
			errAndReport(errBadFastlyAddrFormat.Error(), http.StatusBadRequest)
			return
		}

		m.webResponses.With(prometheus.Labels{httpCode: "200", httpBody: ""}).Inc()
		inbox <- &clientRequest{Addr: addr, Wallet: walletID}
	}
}

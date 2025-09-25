package forwarder

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/datawire/dlib/dlog"
	"github.com/telepresenceio/telepresence/rpc/v2/manager"
	"github.com/telepresenceio/telepresence/v2/pkg/iputil"
	"github.com/telepresenceio/telepresence/v2/pkg/tunnel"
	"github.com/telepresenceio/telepresence/v2/pkg/types"
)

type tcp struct {
	interceptor
}

func newTCP(listenPort uint16, tag tunnel.Tag, target netip.AddrPort) Interceptor {
	return &tcp{
		interceptor: interceptor{
			tag:        tag,
			listenPort: listenPort,
			target:     target,
			lCancel:    func() {},
		},
	}
}

func (f *tcp) Serve(ctx context.Context, initCh chan<- netip.AddrPort) error {
	listener, err := f.listen(ctx)
	if err != nil {
		return err
	}
	defer listener.Close()

	la := listener.Addr().(*net.TCPAddr)
	if initCh != nil {
		initCh <- la.AddrPort()
		close(initCh)
	}

	dlog.Debugf(ctx, "Forwarding from %s", la)
	defer dlog.Debugf(ctx, "Done forwarding from %s", la)

	go f.acceptLoop(listener)
	<-ctx.Done()
	return nil
}

func (f *tcp) listen(ctx context.Context) (*net.TCPListener, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Set up listener lifetime (same as the overall forwarder lifetime)
	f.lCtx, f.lCancel = context.WithCancel(ctx)

	// Set up a target lifetime
	f.tCtx, f.tCancel = context.WithCancel(f.lCtx)
	listenPort := f.listenPort

	listener, err := net.ListenTCP("tcp", &net.TCPAddr{Port: int(listenPort)})
	if err != nil {
		return nil, err
	}
	addr := listener.Addr().(*net.TCPAddr).AddrPort()
	f.lCtx = dlog.WithField(f.lCtx, "listen", addr.String())
	f.listenPort = addr.Port()
	return listener, nil
}

func (f *tcp) acceptLoop(listener *net.TCPListener) {
	for {
		select {
		case <-f.lCtx.Done():
			return
		default:
		}

		conn, err := listener.AcceptTCP()
		if err != nil {
			if f.lCtx.Err() != nil {
				return
			}
			dlog.Infof(f.lCtx, "Error on accept: %+v", err)
			continue
		}
		go func() {
			if err := f.forwardConn(conn); err != nil {
				dlog.Error(f.lCtx, err)
			}
		}()
	}
}

// Number of []byte chunks that can be cached by a wiretap connection before it discards data.
const wiretapCacheSize = 0x100

func (f *tcp) forwardConn(clientConn net.Conn) error {
	var wtIntercepts []*manager.InterceptInfo
	f.mu.Lock()
	ctx := f.tCtx
	targetAddr := f.target
	intercept := f.intercept
	tapCount := len(f.wiretaps)
	if tapCount > 0 {
		wtIntercepts = make([]*manager.InterceptInfo, tapCount)
		i := 0
		for _, wt := range f.wiretaps {
			wtIntercepts[i] = wt
			i++
		}
	}
	f.mu.Unlock()

	// Give mechanism-specific handling a chance first (e.g., HTTP-aware routing)
	if handled, err := f.DispatchByMechanism(ctx, clientConn, intercept); handled || err != nil {
		return err
	}

	ctx = dlog.WithField(ctx, "client", clientConn.RemoteAddr().String())

	// For HTTP intercepts with header requirements, we need to handle them differently
	// Check if we have any HTTP intercepts (either main intercept or wiretaps)
	hasHTTPIntercepts := false
	if intercept != nil && intercept.Spec.Mechanism == "http" && len(intercept.Headers) > 0 {
		dlog.Debugf(ctx, "Found HTTP intercept with headers: %v", intercept.Headers)
		hasHTTPIntercepts = true
	}
	for _, wt := range f.wiretaps {
		if wt.Spec.Mechanism == "http" && len(wt.Headers) > 0 {
			dlog.Debugf(ctx, "Found HTTP wiretap with headers: %v", wt.Headers)
			hasHTTPIntercepts = true
			break
		}
	}
	dlog.Debugf(ctx, "hasHTTPIntercepts: %v, intercept: %v, wiretaps: %d", hasHTTPIntercepts, intercept != nil, len(f.wiretaps))
	if len(f.wiretaps) > 0 {
		var wiretapIDs []string
		for id := range f.wiretaps {
			wiretapIDs = append(wiretapIDs, id)
		}
		dlog.Debugf(ctx, "current wiretap IDs in forwarder: %v", wiretapIDs)
	} else {
		dlog.Debugf(ctx, "no wiretaps found in forwarder, checking forwarder state...")
		// Debug: Check if forwarder has any wiretaps at all
		allWiretapIDs := f.WiretapIDs()
		dlog.Debugf(ctx, "forwarder.WiretapIDs() returns: %v", allWiretapIDs)
	}

	if hasHTTPIntercepts {
		// Use tunnel-based approach for header-based routing with multiple intercepts
		targetHost := f.target.Addr().String()
		targetPort := f.target.Port()
		return f.handleHTTPInterceptWithTunnel(ctx, clientConn, targetHost, targetPort, intercept, wtIntercepts)
	}

	if targetAddr.Port() > 0 {
		if len(wtIntercepts) > 0 {
			var taps []net.Conn
			dlog.Debugf(ctx, "forwarding to %d wiretaps", tapCount)
			clientConn, taps = AddWiretaps(ctx, clientConn, tapCount, wiretapCacheSize)
			wg := sync.WaitGroup{}
			wg.Add(tapCount)
			defer wg.Wait()
			for i, ii := range wtIntercepts {
				go func(conn net.Conn, intercept *manager.InterceptInfo) {
					defer wg.Done()
					dlog.Debugf(ctx, "wiretap to %d", ii.Spec.TargetPort)
					err := f.interceptConn(ctx, conn, intercept)
					if err != nil {
						dlog.Errorf(ctx, "wiretap ended with error: %v", err)
					}
				}(taps[i], ii)
			}
		}
	}
	if intercept != nil {
		return f.interceptConn(ctx, clientConn, intercept)
	}

	defer dlog.Debug(ctx, "Done forwarding")
	defer clientConn.Close()

	if targetAddr.Port() == 0 {
		dlog.Debug(ctx, "Forwarding to /dev/null")
		_, _ = io.Copy(io.Discard, clientConn)
		return nil
	}

	ctx = dlog.WithField(ctx, "target", targetAddr.String())

	dlog.Debug(ctx, "Forwarding...")

	targetConn, err := net.DialTCP("tcp", nil, net.TCPAddrFromAddrPort(targetAddr))
	if err != nil {
		return fmt.Errorf("error on dial: %w", err)
	}
	defer targetConn.Close()

	done := make(chan struct{})

	go func() {
		if _, err := io.Copy(targetConn, clientConn); err != nil && ctx.Err() == nil {
			dlog.Debugf(ctx, "Error clientConn->targetConn: %+v", err)
		}
		_ = targetConn.CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		if _, err := io.Copy(clientConn, targetConn); err != nil && ctx.Err() == nil {
			dlog.Debugf(ctx, "Error targetConn->clientConn: %+v", err)
		}
		if hwCloser, ok := clientConn.(interface{ CloseWrite() error }); ok {
			_ = hwCloser.CloseWrite()
		}
		done <- struct{}{}
	}()

	// Wait for both sides to close the connection
	for numClosed := 0; numClosed < 2; {
		select {
		case <-ctx.Done():
			return nil
		case <-done:
			numClosed++
		}
	}
	return nil
}

func (f *tcp) interceptConn(ctx context.Context, conn net.Conn, iCept *manager.InterceptInfo) error {
	spec := iCept.Spec
	ip, err := iputil.ParseAddr(spec.TargetHost)
	if err != nil {
		return err
	}
	return f.rerouteConn(
		ctx,
		conn,
		tunnel.SessionID(iCept.ClientSession.SessionId),
		netip.AddrPortFrom(ip, uint16(spec.TargetPort)),
		time.Duration(spec.RoundtripLatency),
		time.Duration(spec.DialTimeout))
}

func (f *tcp) rerouteConn(ctx context.Context, conn net.Conn, clientSession tunnel.SessionID, dst netip.AddrPort, latency, timeout time.Duration) error {
	srcAddr := conn.RemoteAddr()
	dlog.Debugf(ctx, "Accept got connection from %s", srcAddr)
	defer dlog.Debugf(ctx, "Done serving connection from %s", srcAddr)

	src, err := iputil.SplitToIPPort(conn.RemoteAddr())
	if err != nil {
		return fmt.Errorf("failed to parse intercept source address %s: %w", srcAddr, err)
	}

	proto, err := types.ParseProto(srcAddr.Network())
	if err != nil {
		return fmt.Errorf("failed to parse intercept protocol %s: %w", srcAddr, err)
	}
	id := tunnel.NewConnID(proto, src, dst)
	ctx, cancel := context.WithCancel(ctx)
	f.mu.Lock()
	sp := f.streamProvider
	f.mu.Unlock()
	s, err := sp.CreateClientStream(ctx, tunnel.AgentToClient, clientSession, id, latency, timeout)
	if err != nil {
		cancel()
		return err
	}

	ingressBytes := tunnel.NewCounterProbe("FromClientBytes")
	egressBytes := tunnel.NewCounterProbe("ToClientBytes")

	// Ingress and egress swap places here, because this endpoint reflects a connection
	// where the stream is attached to a connection *to* the client, not *from* the client.
	d := tunnel.NewConnEndpoint(s, conn, cancel, egressBytes, ingressBytes)
	d.Start(ctx)
	<-d.Done()

	sp.ReportMetrics(ctx, &manager.TunnelMetrics{
		ClientSessionId: string(clientSession),
		IngressBytes:    ingressBytes.GetValue(),
		EgressBytes:     egressBytes.GetValue(),
	})
	return nil
}

// handleHTTPInterceptWithTunnel handles HTTP requests with header-based conditional routing using tunnels
func (f *tcp) handleHTTPInterceptWithTunnel(ctx context.Context, clientConn net.Conn, targetHost string, targetPort uint16, intercept *manager.InterceptInfo, wiretaps []*manager.InterceptInfo) error {
	// Read the HTTP request to inspect headers while preserving the stream
	// We need to read the request and buffer it so we can inspect headers
	// and then replay it to the appropriate destination
	req, requestData, err := f.readAndBufferHTTPRequest(clientConn)
	if err != nil {
		dlog.Errorf(ctx, "Failed to read HTTP request: %v", err)
		return fmt.Errorf("failed to read HTTP request: %w", err)
	}
	dlog.Debugf(ctx, "Successfully read HTTP request: %s %s", req.Method, req.URL.Path)

	// Collect all HTTP intercepts (main intercept + wiretaps)
	var httpIntercepts []*manager.InterceptInfo
	if intercept != nil && intercept.Spec.Mechanism == "http" && len(intercept.Headers) > 0 {
		httpIntercepts = append(httpIntercepts, intercept)
	}
	for _, wt := range wiretaps {
		if wt.Spec.Mechanism == "http" && len(wt.Headers) > 0 {
			httpIntercepts = append(httpIntercepts, wt)
		}
	}

	// Find the best matching intercept based on header patterns
	var matchingIntercept *manager.InterceptInfo
	dlog.Debugf(ctx, "Checking %d HTTP intercepts for header patterns", len(httpIntercepts))
	dlog.Debugf(ctx, "Request headers: %v", req.Header)
	for _, httpIntercept := range httpIntercepts {
		dlog.Debugf(ctx, "Intercept %s has headers: %v", httpIntercept.Id, httpIntercept.Headers)
		// Check if any header matches the patterns
		for headerName, headerValue := range req.Header {
			if len(headerValue) > 0 {
				actualValue := headerValue[0]
				headerPattern := fmt.Sprintf("%s=%s", headerName, actualValue)
				dlog.Debugf(ctx, "Checking header pattern: %s", headerPattern)

				// Check for exact match first
				if _, exists := httpIntercept.Headers[headerPattern]; exists {
					matchingIntercept = httpIntercept
					dlog.Debugf(ctx, "Request header %s=%s matches pattern for intercept %s, routing to port %d", headerName, actualValue, httpIntercept.Id, httpIntercept.Spec.TargetPort)
					break
				}

				// Check for case-insensitive match
				lowerHeaderPattern := fmt.Sprintf("%s=%s", strings.ToLower(headerName), actualValue)
				if _, exists := httpIntercept.Headers[lowerHeaderPattern]; exists {
					matchingIntercept = httpIntercept
					dlog.Debugf(ctx, "Request header %s=%s matches pattern (case-insensitive) for intercept %s, routing to port %d", headerName, actualValue, httpIntercept.Id, httpIntercept.Spec.TargetPort)
					break
				} else {
					dlog.Debugf(ctx, "Header pattern %s not found in intercept headers", headerPattern)
				}
			}
		}
		if matchingIntercept != nil {
			break
		}
	}

	if matchingIntercept != nil {
		// Route to the matching intercept using the existing tunnel mechanism
		// The port is already set in the intercept spec from the --port flag
		replayConn := &replayConn{
			Conn:        clientConn,
			requestData: requestData,
		}
		return f.interceptConn(ctx, replayConn, matchingIntercept)
	} else {
		dlog.Debugf(ctx, "Request does not match any HTTP intercept headers, routing to original service")
		// Route to original service using direct connection (no intercept)
		// Don't modify the intercept state when routing to original service
		// This prevents state corruption that causes intermittent failures
		replayConn := &replayConn{
			Conn:        clientConn,
			requestData: requestData,
		}
		return f.forwardToOriginalServiceDirect(ctx, replayConn, targetHost, targetPort)
	}
}

// readAndBufferHTTPRequest reads an HTTP request and returns both the parsed request and the raw data
func (f *tcp) readAndBufferHTTPRequest(conn net.Conn) (*http.Request, []byte, error) {
	// Create a buffered reader to avoid data corruption issues
	var requestData bytes.Buffer
	teeReader := io.TeeReader(conn, &requestData)
	bufferedReader := bufio.NewReader(teeReader)

	req, err := http.ReadRequest(bufferedReader)
	if err != nil {
		return nil, nil, err
	}

	// Read the body if present
	if req.ContentLength > 0 {
		body := make([]byte, req.ContentLength)
		_, err = io.ReadFull(bufferedReader, body)
		if err != nil {
			return nil, nil, err
		}
	}

	return req, requestData.Bytes(), nil
}

// replayConn is a connection that replays buffered data first, then forwards to the underlying connection
type replayConn struct {
	net.Conn
	requestData []byte
	replayed    bool
}

func (r *replayConn) Read(b []byte) (n int, err error) {
	if !r.replayed && len(r.requestData) > 0 {
		// Replay the buffered request data
		n = copy(b, r.requestData)
		r.requestData = r.requestData[n:]
		if len(r.requestData) == 0 {
			r.replayed = true
		}
		return n, nil
	}
	// After replaying, read from the underlying connection
	return r.Conn.Read(b)
}

// Write method to ensure proper connection handling
func (r *replayConn) Write(b []byte) (n int, err error) {
	return r.Conn.Write(b)
}

// Close method to ensure proper connection cleanup
func (r *replayConn) Close() error {
	return r.Conn.Close()
}

// LocalAddr method for connection state consistency
func (r *replayConn) LocalAddr() net.Addr {
	return r.Conn.LocalAddr()
}

// RemoteAddr method for connection state consistency
func (r *replayConn) RemoteAddr() net.Addr {
	return r.Conn.RemoteAddr()
}

// SetDeadline method for connection state consistency
func (r *replayConn) SetDeadline(t time.Time) error {
	return r.Conn.SetDeadline(t)
}

// SetReadDeadline method for connection state consistency
func (r *replayConn) SetReadDeadline(t time.Time) error {
	return r.Conn.SetReadDeadline(t)
}

// SetWriteDeadline method for connection state consistency
func (r *replayConn) SetWriteDeadline(t time.Time) error {
	return r.Conn.SetWriteDeadline(t)
}

// forwardToOriginalServiceDirect forwards the request to the original service using direct connection
func (f *tcp) forwardToOriginalServiceDirect(ctx context.Context, clientConn net.Conn, targetHost string, targetPort uint16) error {
	// Connect to the original service
	targetAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", targetHost, targetPort))
	if err != nil {
		return fmt.Errorf("error resolving target address: %w", err)
	}

	targetConn, err := net.DialTCP("tcp", nil, targetAddr)
	if err != nil {
		return fmt.Errorf("error connecting to target: %w", err)
	}
	defer targetConn.Close()

	// Copy data bidirectionally
	done := make(chan struct{})

	go func() {
		if _, err := io.Copy(targetConn, clientConn); err != nil && ctx.Err() == nil {
			dlog.Debugf(ctx, "Error clientConn->targetConn: %+v", err)
		}
		_ = targetConn.CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		if _, err := io.Copy(clientConn, targetConn); err != nil && ctx.Err() == nil {
			dlog.Debugf(ctx, "Error targetConn->clientConn: %+v", err)
		}
		if hwCloser, ok := clientConn.(interface{ CloseWrite() error }); ok {
			_ = hwCloser.CloseWrite()
		}
		done <- struct{}{}
	}()

	// Wait for both sides to close the connection
	for numClosed := 0; numClosed < 2; {
		select {
		case <-ctx.Done():
			return nil
		case <-done:
			numClosed++
		}
	}
	return nil
}

// DispatchByMechanism implements mechanism-specific per-connection dispatch for TCP.
// It currently only routes HTTP-aware intercepts when requested.
func (f *tcp) DispatchByMechanism(ctx context.Context, clientConn net.Conn, intercept *manager.InterceptInfo) (bool, error) {
	var spec *manager.InterceptSpec
	if intercept != nil {
		spec = intercept.Spec
	}

	// Check if this is an HTTP intercept with header patterns
	hasHTTPHeaders := spec != nil && spec.Mechanism == "http" && len(intercept.Headers) > 0

	if hasHTTPHeaders {
		// Collect wiretaps under lock to maintain existing behavior
		f.mu.Lock()
		target := f.target
		tapCount := len(f.wiretaps)
		var wtIntercepts []*manager.InterceptInfo
		if tapCount > 0 {
			wtIntercepts = make([]*manager.InterceptInfo, tapCount)
			i := 0
			for _, wt := range f.wiretaps {
				wtIntercepts[i] = wt
				i++
			}
		}
		f.mu.Unlock()

		targetHost := target.Addr().String()
		targetPort := target.Port()
		err := f.handleHTTPInterceptWithTunnel(ctx, clientConn, targetHost, targetPort, intercept, wtIntercepts)
		return true, err
	}
	return false, nil
}

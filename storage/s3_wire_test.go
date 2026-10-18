package storage

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestS3ConditionalUploadWaitsForContinue(t *testing.T) {
	for _, test := range []struct {
		name    string
		content []byte
	}{
		{"small", []byte("private document")},
		{"large", bytes.Repeat([]byte("<html>private uploaded document</html>\x00"), 33000)},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
				t.Fatal(err)
			}
			// A raw server can reject the first name without consuming its body.
			// An ordinary handler may drain it or send 100 Continue while reading.
			serve := func() error {
				for attempt := range 2 {
					if err := func() error {
						connection, err := listener.Accept()
						if err != nil {
							return err
						}
						var request *http.Request
						defer func() {
							_ = connection.Close()
							if request != nil {
								_ = request.Body.Close()
							}
						}()
						if err := connection.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
							return err
						}
						wire := bufio.NewReader(connection)
						request, err = http.ReadRequest(wire)
						if err != nil {
							return err
						}
						name := []string{"document.html", "document_AAAAAAA.html"}[attempt]
						if request.Method != http.MethodPut || request.URL.Path != "/private-bucket/"+name || request.Header.Get("If-None-Match") != "*" || request.Header.Get("Expect") != "100-continue" || request.Header.Get("X-Amz-Checksum-Sha256") != s3TestChecksum(test.content) || request.ContentLength != int64(len(test.content)) {
							return fmt.Errorf("attempt %d lost conditional PUT headers, name or checksum", attempt)
						}
						// The client's wait is much longer than this observation. Merely
						// adding Expect without enabling its HTTP transport gate fails.
						if err := connection.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
							return err
						}
						data, err := wire.Peek(1)
						if timeout, ok := err.(net.Error); len(data) != 0 || !ok || !timeout.Timeout() {
							return fmt.Errorf("attempt %d sent a body before the server accepted it", attempt)
						}
						if err := connection.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
							return err
						}
						recorder := httptest.NewRecorder()
						if attempt == 0 {
							s3TestError(recorder, http.StatusPreconditionFailed, "PreconditionFailed")
						} else {
							if _, err := io.WriteString(connection, "HTTP/1.1 100 Continue\r\n\r\n"); err != nil {
								return err
							}
							content, err := io.ReadAll(request.Body)
							if err != nil || !bytes.Equal(content, test.content) {
								return fmt.Errorf("accepted PUT lost complete content: %v", err)
							}
							s3TestPutResponse(recorder, content)
						}
						response := recorder.Result()
						defer response.Body.Close()
						response.Close = true
						return response.Write(connection)
					}(); err != nil {
						return err
					}
				}
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- serve() }()
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.ExpectContinueTimeout = 30 * time.Second
			defer transport.CloseIdleConnections()
			config := s3TestConfig()
			config.Endpoint, config.AllowLoopbackHTTP = "http://"+listener.Addr().String(), true
			config.Transport, config.RequestTimeout = transport, 10*time.Second
			config.Random = s3ReadFunc(func(p []byte) (int, error) { clear(p); return len(p), nil })
			backend, err := NewS3(config)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			info, saveErr := backend.Save(t.Context(), "document.html", bytes.NewReader(test.content), SaveOptions{})
			_ = listener.Close()
			if err := <-done; err != nil {
				t.Error(err)
			}
			if saveErr != nil || !info.Valid() || info.Name() != "document_AAAAAAA.html" || info.Size() != int64(len(test.content)) {
				t.Fatal("conditional publication did not preserve the collision and accepted body", saveErr)
			}
		})
	}
}

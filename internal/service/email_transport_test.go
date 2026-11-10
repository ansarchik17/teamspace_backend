package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/west6ide/workspace-backend/internal/config"
)

func TestSMTPTransportImplicitTLS(t *testing.T) {
	for _, trustFixture := range []bool{true, false} {
		t.Run(fmt.Sprint("trustFixture=", trustFixture), func(t *testing.T) {
			fixture := httptest.NewTLSServer(nil)
			certificate := fixture.TLS.Certificates[0]
			fixture.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			commands := make(chan []string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					commands <- nil
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
				if err := secure.Handshake(); err != nil {
					commands <- nil
					return
				}
				defer secure.Close()
				fmt.Fprint(secure, "220 test SMTP\r\n")
				reader := bufio.NewReader(secure)
				seen := []string{}
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						commands <- seen
						return
					}
					line = strings.TrimSpace(line)
					seen = append(seen, line)
					switch {
					case strings.HasPrefix(line, "EHLO"):
						fmt.Fprint(secure, "250-test\r\n250 AUTH PLAIN\r\n")
					case strings.HasPrefix(line, "AUTH PLAIN"):
						fmt.Fprint(secure, "235 authenticated\r\n")
					case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
						fmt.Fprint(secure, "250 accepted\r\n")
					case line == "DATA":
						fmt.Fprint(secure, "354 continue\r\n")
						for {
							part, e := reader.ReadString('\n')
							if e != nil {
								commands <- seen
								return
							}
							if part == ".\r\n" {
								break
							}
						}
						fmt.Fprint(secure, "250 queued\r\n")
					case line == "QUIT":
						fmt.Fprint(secure, "221 bye\r\n")
						commands <- seen
						return
					default:
						commands <- seen
						return
					}
				}
			}()
			host, portText, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.Atoi(portText)
			cfg := config.SMTPConfig{Host: host, Port: port, ImplicitTLS: true, UseTLS: true, TLSServerName: "mail.example.test", SkipTLSVerify: trustFixture, Username: "sender", Password: "test-only"}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = (defaultSMTPTransport{}).Send(ctx, cfg, "sender@example.test", []string{"receiver@example.test"}, []byte("Subject: Test\r\n\r\nMessage\r\n"))
			seen := <-commands
			if !trustFixture {
				if err == nil || len(seen) != 0 {
					t.Fatalf("untrusted TLS allowed SMTP commands: err=%v commands=%v", err, seen)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(seen, "\n")
			if !strings.Contains(joined, "AUTH PLAIN") || !strings.Contains(joined, "RCPT TO:<receiver@example.test>") || !strings.Contains(joined, "DATA") {
				t.Fatalf("incomplete SMTP transaction: %v", seen)
			}
			if strings.Contains(joined, "STARTTLS") {
				t.Fatal("implicit TLS must not start TLS twice")
			}
		})
	}
}

func TestSMTPTransportCancellationClosesSilentServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan struct{})
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			close(closed)
			return
		}
		defer conn.Close()
		_, _ = conn.Read(make([]byte, 1))
		close(closed)
	}()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	done := make(chan error, 1)
	go func() {
		done <- (defaultSMTPTransport{}).Send(ctx, config.SMTPConfig{Host: host, Port: port}, "a@example.test", nil, nil)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled send succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled SMTP connection stayed blocked")
	}
	<-closed
}

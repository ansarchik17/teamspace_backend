package service

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/require"
	"github.com/west6ide/workspace-backend/internal/domain"
)

func ldapTestMessage(id int64, op *ber.Packet, paging *ldap.ControlPaging) *ber.Packet {
	message := ber.NewSequence("LDAPMessage")
	message.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, "messageID"))
	message.AppendChild(op)
	if paging != nil {
		controls := ber.Encode(ber.ClassContext, ber.TypeConstructed, 0, nil, "controls")
		controls.AppendChild(paging.Encode())
		message.AppendChild(controls)
	}
	return message
}
func ldapTestResult(tag ber.Tag, code int) *ber.Packet {
	result := ber.Encode(ber.ClassApplication, ber.TypeConstructed, tag, nil, "result")
	result.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, "resultCode"))
	result.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "matchedDN"))
	result.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "diagnosticMessage"))
	return result
}
func ldapTestEntry(name string) *ber.Packet {
	entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, 4, nil, "entry")
	entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "cn="+name+",dc=example,dc=com", "dn"))
	attrs := ber.NewSequence("attributes")
	for attribute, value := range map[string]string{"entryUUID": name + "-uuid", "uid": name, "mail": name + "@example.com", "givenName": name, "sn": "Test"} {
		a := ber.NewSequence("attribute")
		a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, attribute, "type"))
		values := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "values")
		values.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, value, "value"))
		a.AppendChild(values)
		attrs.AppendChild(a)
	}
	entry.AppendChild(attrs)
	return entry
}

func TestLDAPConnectorTLSAndPaging(t *testing.T) {
	for _, mode := range []string{"success", "starttls", "partial_error", "untrusted_tls"} {
		t.Run(mode, func(t *testing.T) {
			certSource := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			certificate := certSource.TLS.Certificates[0]
			certSource.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			secure := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
			done := make(chan int, 1)
			go func() {
				var conn net.Conn
				var e error
				if mode == "starttls" {
					conn, e = listener.Accept()
				} else {
					conn, e = secure.Accept()
				}
				if e != nil {
					done <- 0
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				pages := 0
				defer func() { done <- pages }()
				for {
					packet, e := ber.ReadPacket(conn)
					if e != nil {
						return
					}
					if len(packet.Children) < 2 {
						return
					}
					id, ok := packet.Children[0].Value.(int64)
					if !ok {
						return
					}
					op := packet.Children[1]
					switch op.Tag {
					case 23:
						_, _ = conn.Write(ldapTestMessage(id, ldapTestResult(24, 0), nil).Bytes())
						encrypted := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
						if encrypted.Handshake() != nil {
							return
						}
						conn = encrypted
					case 0:
						_, _ = conn.Write(ldapTestMessage(id, ldapTestResult(1, 0), nil).Bytes())
					case 3:
						pages++
						if mode == "partial_error" && pages == 2 {
							_, _ = conn.Write(ldapTestMessage(id, ldapTestResult(5, 4), nil).Bytes())
							return
						}
						name := "alice"
						if pages == 2 {
							name = "bob"
						}
						_, _ = conn.Write(ldapTestMessage(id, ldapTestEntry(name), nil).Bytes())
						paging := ldap.NewControlPaging(250)
						if pages == 1 {
							paging.SetCookie([]byte{1})
						}
						_, _ = conn.Write(ldapTestMessage(id, ldapTestResult(5, 0), paging).Bytes())
						if pages == 2 {
							return
						}
					default:
						return
					}
				}
			}()
			host := listener.Addr().String()
			t.Setenv("CONTACTS_LDAP_ALLOWED_HOSTS", host)
			ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}))
			if mode == "untrusted_tls" {
				ca = ""
			}
			scheme := "ldaps://"
			if mode == "starttls" {
				scheme = "ldap://"
			}
			settings, err := normalizeDirectorySettings(domain.DirectorySettings{Type: "ldap", URL: scheme + host, BaseDN: "dc=example,dc=com", BindDN: "cn=reader,dc=example,dc=com", CACertPEM: ca})
			require.NoError(t, err)
			rows, err := fetchLDAPContacts(context.Background(), settings, "test-password")
			if mode == "success" || mode == "starttls" {
				require.NoError(t, err)
				require.Len(t, rows, 2)
				require.Equal(t, "alice-uuid", rows[0].ExternalID)
				require.Equal(t, "bob", rows[1].Username)
			} else {
				require.Error(t, err)
				require.Nil(t, rows)
				require.False(t, strings.Contains(err.Error(), "test-password"))
			}
			select {
			case pages := <-done:
				if mode != "untrusted_tls" {
					require.Equal(t, 2, pages)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("LDAP test server did not finish")
			}
		})
	}
}

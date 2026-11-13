package tls_test

import (
	gotls "crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/protocol/tls/cert"
	. "github.com/xtls/xray-core/transport/internet/tls"
)

// A certificate file that cannot be read at one reload tick (it is being replaced, the
// mount is not ready) must not switch hot reload off for the rest of the process: the
// pair written afterwards has to be served. Upstream returned from the reload goroutine
// on the first read error, so a renewed certificate was never picked up.
func TestHotReloadSurvivesReadError(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")

	writePair := func(name string) (certPEM, keyPEM []byte) {
		ct, _ := cert.MustGenerate(nil, cert.CommonName(name), cert.DNSNames(name))
		certPEM, keyPEM = ct.ToPEM()
		common.Must(os.WriteFile(keyPath, keyPEM, 0o600))
		common.Must(os.WriteFile(certPath, certPEM, 0o600))
		return
	}

	certPEM, keyPEM := writePair("old.example.com")
	c := &Config{
		Certificate: []*Certificate{{
			Certificate:     certPEM,
			Key:             keyPEM,
			CertificatePath: certPath,
			KeyPath:         keyPath,
			OcspStapling:    1, // reload tick of 1 second (the smallest the config allows)
		}},
	}
	tlsConfig := c.GetTLSConfig()
	served := func() string {
		kp, err := tlsConfig.GetCertificate(&gotls.ClientHelloInfo{ServerName: "any"})
		common.Must(err)
		return kp.Leaf.Subject.CommonName
	}
	if got := served(); got != "old.example.com" {
		t.Fatal("unexpected initial certificate: ", got)
	}

	// The file is missing for at least one tick...
	common.Must(os.Remove(certPath))
	time.Sleep(1500 * time.Millisecond)
	if got := served(); got != "old.example.com" {
		t.Fatal("the current certificate must stay in use while the file is unreadable, got: ", got)
	}

	// ...and then the renewed pair appears.
	writePair("new.example.com")
	deadline := time.Now().Add(5 * time.Second)
	for served() != "new.example.com" {
		if time.Now().After(deadline) {
			t.Fatal("renewed certificate was not picked up after a read error, still serving: ", served())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

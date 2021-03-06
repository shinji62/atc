package acceptance_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/gomega/gexec"
)

const BASIC_AUTH = "basic"
const BASIC_AUTH_NO_PASSWORD = "basic-no-password"
const BASIC_AUTH_NO_USERNAME = "basic-no-username"
const GITHUB_AUTH = "github"
const GITHUB_ENTERPRISE_AUTH = "github-enterprise"
const UAA_AUTH = "cf"
const UAA_AUTH_NO_CLIENT_SECRET = "cf-no-secret"
const UAA_AUTH_NO_TOKEN_URL = "cf-no-token-url"
const UAA_AUTH_NO_SPACE = "cf-no-space"
const NOT_CONFIGURED_AUTH = "not-configured"
const DEVELOPMENT_MODE = "dev"
const NO_AUTH = DEVELOPMENT_MODE

type ATCCommand struct {
	atcBin                 string
	atcServerNumber        uint16
	tlsFlags               []string
	authTypes              []string
	postgresDataSourceName string
	pemPrivateKey          string

	process                    ifrit.Process
	port                       uint16
	tlsPort                    uint16
	tlsCertificateOrganization string
	tmpDir                     string
}

func NewATCCommand(
	atcBin string,
	atcServerNumber uint16,
	postgresDataSourceName string,
	tlsFlags []string,
	authTypes ...string,
) *ATCCommand {
	return &ATCCommand{
		atcBin:                     atcBin,
		atcServerNumber:            atcServerNumber,
		postgresDataSourceName:     postgresDataSourceName,
		tlsFlags:                   tlsFlags,
		authTypes:                  authTypes,
		tlsCertificateOrganization: "Acme Co",
		pemPrivateKey: `-----BEGIN RSA PRIVATE KEY-----
MIIBOgIBAAJBALKZD0nEffqM1ACuak0bijtqE2QrI/KLADv7l3kK3ppMyCuLKoF0
fd7Ai2KW5ToIwzFofvJcS/STa6HA5gQenRUCAwEAAQJBAIq9amn00aS0h/CrjXqu
/ThglAXJmZhOMPVn4eiu7/ROixi9sex436MaVeMqSNf7Ex9a8fRNfWss7Sqd9eWu
RTUCIQDasvGASLqmjeffBNLTXV2A5g4t+kLVCpsEIZAycV5GswIhANEPLmax0ME/
EO+ZJ79TJKN5yiGBRsv5yvx5UiHxajEXAiAhAol5N4EUyq6I9w1rYdhPMGpLfk7A
IU2snfRJ6Nq2CQIgFrPsWRCkV+gOYcajD17rEqmuLrdIRexpg8N1DOSXoJ8CIGlS
tAboUGBxTDq3ZroNism3DaMIbKPyYrAqhKov1h5V
-----END RSA PRIVATE KEY-----`,
	}
}

func (a *ATCCommand) URL(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", a.port, path)
}

func (a *ATCCommand) TLSURL(path string) string {
	return fmt.Sprintf("https://127.0.0.1:%d%s", a.tlsPort, path)
}

func (a *ATCCommand) Start() error {
	var err error
	a.tmpDir, err = ioutil.TempDir("", "")
	if err != nil {
		return err
	}

	err = a.createCert()
	if err != nil {
		return err
	}

	atcCommand := a.getATCCommand()
	atcRunner := ginkgomon.New(ginkgomon.Config{
		Command:       atcCommand,
		Name:          "atc",
		StartCheck:    "atc.listening",
		AnsiColorCode: "32m",
	})

	a.process = ginkgomon.Invoke(atcRunner)

	return nil
}

func (a *ATCCommand) StartAndWait() (*gexec.Session, error) {
	var err error
	a.tmpDir, err = ioutil.TempDir("", "")
	if err != nil {
		return nil, err
	}

	err = a.createCert()
	if err != nil {
		return nil, err
	}

	return gexec.Start(a.getATCCommand(), GinkgoWriter, GinkgoWriter)
}

func (a *ATCCommand) Stop() {
	ginkgomon.Interrupt(a.process)
	os.RemoveAll(a.tmpDir)
}

func (a *ATCCommand) getATCCommand() *exec.Cmd {
	a.port = 5697 + uint16(GinkgoParallelNode()) + (a.atcServerNumber * 100)
	debugPort := 6697 + uint16(GinkgoParallelNode()) + (a.atcServerNumber * 100)

	params := []string{
		"--bind-port", fmt.Sprintf("%d", a.port),
		"--debug-bind-port", fmt.Sprintf("%d", debugPort),
		"--peer-url", fmt.Sprintf("http://127.0.0.1:%d", a.port),
		"--postgres-data-source", a.postgresDataSourceName,
		"--external-url", fmt.Sprintf("http://127.0.0.1:%d", a.port),
	}

	for _, authType := range a.authTypes {
		switch authType {
		case BASIC_AUTH:
			params = append(params,
				"--basic-auth-username", "admin",
				"--basic-auth-password", "password",
			)
		case BASIC_AUTH_NO_PASSWORD:
			params = append(params,
				"--basic-auth-username", "admin",
			)
		case BASIC_AUTH_NO_USERNAME:
			params = append(params,
				"--basic-auth-password", "password",
			)
		case GITHUB_AUTH:
			params = append(params,
				"--github-auth-client-id", "admin",
				"--github-auth-client-secret", "password",
				"--github-auth-organization", "myorg",
				"--github-auth-team", "myorg/all",
				"--github-auth-user", "myuser",
			)
		case GITHUB_ENTERPRISE_AUTH:
			params = append(params,
				"--github-auth-client-id", "admin",
				"--github-auth-client-secret", "password",
				"--github-auth-organization", "myorg",
				"--github-auth-team", "myorg/all",
				"--github-auth-user", "myuser",
				"--github-auth-auth-url", "https://github.example.com/login/oauth/authorize",
				"--github-auth-token-url", "https://github.example.com/login/oauth/access_token",
				"--github-auth-api-url", "https://github.example.com/api/v3/",
			)
		case UAA_AUTH:
			params = append(params,
				"--uaa-auth-client-id", "admin",
				"--uaa-auth-client-secret", "password",
				"--uaa-auth-cf-space", "myspace",
				"--uaa-auth-auth-url", "https://uaa.example.com/oauth/authorize",
				"--uaa-auth-token-url", "https://uaa.example.com/oauth/token",
				"--uaa-auth-cf-url", "https://cf.example.com/api",
			)
		case UAA_AUTH_NO_CLIENT_SECRET:
			params = append(params,
				"--uaa-auth-client-id", "admin",
			)
		case UAA_AUTH_NO_SPACE:
			params = append(params,
				"--uaa-auth-client-id", "admin",
				"--uaa-auth-client-secret", "password",
			)
		case UAA_AUTH_NO_TOKEN_URL:
			params = append(params,
				"--uaa-auth-client-id", "admin",
				"--uaa-auth-client-secret", "password",
				"--uaa-auth-cf-space", "myspace",
				"--uaa-auth-auth-url", "https://uaa.example.com/oauth/authorize",
				"--uaa-auth-cf-url", "https://cf.example.com/api",
			)
		case DEVELOPMENT_MODE:
			params = append(params, "--development-mode")
		case NOT_CONFIGURED_AUTH:
		default:
			panic("unknown auth type")
		}
	}

	if len(a.tlsFlags) > 0 {
		a.tlsPort = 7697 + uint16(GinkgoParallelNode()) + (a.atcServerNumber * 100)
		params = append(params, "--external-url", fmt.Sprintf("https://127.0.0.1:%d/", a.tlsPort))

		for _, tlsFlag := range a.tlsFlags {
			switch tlsFlag {
			case "--tls-bind-port":
				params = append(params, "--tls-bind-port", fmt.Sprintf("%d", a.tlsPort))
			case "--tls-cert":
				params = append(params, "--tls-cert", filepath.Join(a.tmpDir, "server.pem"))
			case "--tls-key":
				params = append(params, "--tls-key", filepath.Join(a.tmpDir, "server.key"))
			}
		}
	}

	return exec.Command(a.atcBin, params...)
}

func (a *ATCCommand) createCert() error {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		log.Fatalf("failed to generate serial number: %s", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{a.tlsCertificateOrganization},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(24 * time.Hour),

		IsCA:                  true,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses: []net.IP{
			net.IP{127, 0, 0, 1},
		},
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &(priv.PublicKey), priv)
	if err != nil {
		return err
	}

	certOut, err := os.Create(filepath.Join(a.tmpDir, "server.pem"))
	if err != nil {
		return err
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()

	keyOut, err := os.OpenFile(filepath.Join(a.tmpDir, "server.key"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	pemBlockForKey := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	pem.Encode(keyOut, pemBlockForKey)
	keyOut.Close()

	return nil
}

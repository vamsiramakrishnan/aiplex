package deploy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

func inCluster() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != "" && os.Getenv("KUBERNETES_SERVICE_PORT") != ""
}

func newHTTPClient(tlsConfig *tls.Config) *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig:     tlsConfig,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}}
}

// loadInClusterConn uses the pod's service account: the API server address
// from the environment, the cluster CA, and the projected token (re-read on
// every request because it rotates).
func loadInClusterConn() (*clusterConn, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in a cluster (KUBERNETES_SERVICE_HOST/PORT unset)")
	}
	caPEM, err := os.ReadFile(filepath.Join(serviceAccountDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("read service account CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("service account CA has no certificates")
	}
	tokenPath := filepath.Join(serviceAccountDir, "token")
	if _, err := os.Stat(tokenPath); err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	ns := ""
	if b, err := os.ReadFile(filepath.Join(serviceAccountDir, "namespace")); err == nil {
		ns = strings.TrimSpace(string(b))
	}
	return &clusterConn{
		base: "https://" + net.JoinHostPort(host, port),
		hc:   newHTTPClient(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}),
		token: func() (string, error) {
			b, err := os.ReadFile(tokenPath)
			if err != nil {
				return "", fmt.Errorf("read service account token: %w", err)
			}
			return strings.TrimSpace(string(b)), nil
		},
		namespace: ns,
	}, nil
}

type kubeconfigFile struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                string `yaml:"server"`
			CertificateAuthority  string `yaml:"certificate-authority"`
			CertificateAuthorityD string `yaml:"certificate-authority-data"`
			InsecureSkipTLSVerify bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token         string         `yaml:"token"`
			TokenFile     string         `yaml:"tokenFile"`
			ClientCert    string         `yaml:"client-certificate"`
			ClientCertD   string         `yaml:"client-certificate-data"`
			ClientKey     string         `yaml:"client-key"`
			ClientKeyD    string         `yaml:"client-key-data"`
			Exec          map[string]any `yaml:"exec"`
			AuthProvider  map[string]any `yaml:"auth-provider"`
			Username      string         `yaml:"username"`
			ImpersonateAs string         `yaml:"as"`
		} `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
}

// loadKubeconfigConn reads the current context of a kubeconfig. It supports
// the credentials that need no helper process: a bearer token (or token file)
// or a client certificate, with the cluster CA. Kubeconfigs that rely on an
// exec plugin or auth provider (for example `gke-gcloud-auth-plugin`) are
// refused with a message, since running arbitrary helper commands from the API
// process is not something this client does; point it at a kubeconfig with a
// token, or use the in-cluster path. Insecure TLS is refused.
func loadKubeconfigConn(path string) (*clusterConn, error) {
	if path == "" {
		path = os.Getenv("KUBECONFIG")
		if i := strings.IndexRune(path, os.PathListSeparator); i >= 0 {
			path = path[:i]
		}
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("no kubeconfig and not in a cluster: %w", err)
		}
		path = filepath.Join(home, ".kube", "config")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("not in a cluster and cannot read kubeconfig: %w", err)
	}
	var kc kubeconfigFile
	if err := yaml.Unmarshal(raw, &kc); err != nil {
		return nil, fmt.Errorf("kubeconfig %s is not valid YAML", path)
	}
	var clusterName, userName, ns string
	for _, c := range kc.Contexts {
		if c.Name == kc.CurrentContext {
			clusterName, userName, ns = c.Context.Cluster, c.Context.User, c.Context.Namespace
		}
	}
	if clusterName == "" {
		return nil, fmt.Errorf("kubeconfig %s has no usable current-context", path)
	}
	if ns == "" {
		ns = "default"
	}
	base := filepath.Dir(path)
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	data := func(inline, file string) ([]byte, error) {
		if inline != "" {
			return base64.StdEncoding.DecodeString(inline)
		}
		if file != "" {
			return os.ReadFile(abs(file))
		}
		return nil, nil
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	var server string
	found := false
	for _, c := range kc.Clusters {
		if c.Name != clusterName {
			continue
		}
		found = true
		server = c.Cluster.Server
		if c.Cluster.InsecureSkipTLSVerify {
			return nil, fmt.Errorf("kubeconfig cluster %q disables TLS verification, which is refused", clusterName)
		}
		ca, err := data(c.Cluster.CertificateAuthorityD, c.Cluster.CertificateAuthority)
		if err != nil {
			return nil, fmt.Errorf("kubeconfig cluster %q CA: %w", clusterName, err)
		}
		if ca != nil {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(ca) {
				return nil, fmt.Errorf("kubeconfig cluster %q CA has no certificates", clusterName)
			}
			tlsCfg.RootCAs = pool
		}
	}
	if !found || !strings.HasPrefix(server, "https://") {
		return nil, fmt.Errorf("kubeconfig cluster %q must have an https server", clusterName)
	}

	token := ""
	tokenFile := ""
	for _, u := range kc.Users {
		if u.Name != userName {
			continue
		}
		if len(u.User.Exec) > 0 || len(u.User.AuthProvider) > 0 || u.User.Username != "" || u.User.ImpersonateAs != "" {
			return nil, fmt.Errorf("kubeconfig user %q uses an exec plugin, auth provider, basic auth or impersonation, which the API's built-in client does not run; use a token or client certificate, or run in-cluster", userName)
		}
		token, tokenFile = u.User.Token, abs(u.User.TokenFile)
		cert, err := data(u.User.ClientCertD, u.User.ClientCert)
		if err != nil {
			return nil, fmt.Errorf("kubeconfig user %q certificate: %w", userName, err)
		}
		key, err := data(u.User.ClientKeyD, u.User.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("kubeconfig user %q key: %w", userName, err)
		}
		if cert != nil && key != nil {
			pair, err := tls.X509KeyPair(cert, key)
			if err != nil {
				return nil, fmt.Errorf("kubeconfig user %q client certificate: %w", userName, err)
			}
			tlsCfg.Certificates = []tls.Certificate{pair}
		}
	}
	if token == "" && tokenFile == "" && len(tlsCfg.Certificates) == 0 {
		return nil, fmt.Errorf("kubeconfig user %q has no token or client certificate", userName)
	}
	return &clusterConn{
		base: strings.TrimRight(server, "/"),
		hc:   newHTTPClient(tlsCfg),
		token: func() (string, error) {
			if tokenFile != "" {
				b, err := os.ReadFile(tokenFile)
				if err != nil {
					return "", fmt.Errorf("read kubeconfig token file: %w", err)
				}
				return strings.TrimSpace(string(b)), nil
			}
			return token, nil
		},
		namespace: ns,
	}, nil
}

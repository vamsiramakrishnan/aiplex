package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// fieldManager is the server-side-apply field manager for everything the
// deploy engine renders. It is fixed so repeated applies converge on one
// owner; force=true means the engine wins conflicts with other managers, as
// `kubectl apply --server-side --force-conflicts` did.
const fieldManager = "aiplex-deploy"

// maxStatusBody bounds how much of an API server reply is read or reported.
const maxStatusBody = 1 << 20

// LiveK8sClient applies manifests to a real cluster through the Kubernetes API:
// server-side apply (PATCH, application/apply-patch+yaml) and DELETE, over
// HTTPS with the pod's service-account token or a simple kubeconfig.
//
// It is a deliberately small REST client rather than client-go. client-go
// (dynamic client, discovery, RESTMapper, clientcmd) links to a ~35 MB
// stripped binary on its own, measured, and the API binary is ~33 MB, so
// adopting it would roughly double the binary to make two kinds of HTTP call.
// The protocol surface used here is stable: GET /api/v1 or
// /apis/{group}/{version} to map a Kind to its resource (readable by every
// authenticated subject through system:discovery), PATCH to apply, DELETE to
// remove.
//
// Every operation is namespaced. A Kind the API reports as cluster-scoped is
// refused, matching the namespaced Role in the Helm chart.
type LiveK8sClient struct {
	kubeconfig string
	dryRun     bool

	// override is set by WithAPIServer (tests, proxies); otherwise the
	// connection is resolved lazily from the in-cluster environment or the
	// kubeconfig, so constructing a client never touches the network or disk.
	override *clusterConn

	mu        sync.Mutex
	conn      *clusterConn
	resources map[string]resourceInfo
}

// LiveK8sOption configures the live K8s client.
type LiveK8sOption func(*LiveK8sClient)

// WithKubeconfig sets a custom kubeconfig path.
func WithKubeconfig(path string) LiveK8sOption {
	return func(c *LiveK8sClient) { c.kubeconfig = path }
}

// WithDryRun enables dry-run mode (generates manifests without applying).
func WithDryRun(dryRun bool) LiveK8sOption {
	return func(c *LiveK8sClient) { c.dryRun = dryRun }
}

// WithAPIServer points the client at an explicit API server with a bearer
// token and HTTP client. It is for tests and for API servers reached through a
// proxy; the default is in-cluster configuration, then the kubeconfig.
func WithAPIServer(base string, hc *http.Client, token, namespace string) LiveK8sOption {
	return func(c *LiveK8sClient) {
		c.override = &clusterConn{
			base: strings.TrimRight(base, "/"), hc: hc, namespace: namespace,
			token: func() (string, error) { return token, nil },
		}
	}
}

// NewLiveK8sClientConfigured creates a K8s client with options. It performs no
// I/O; the cluster connection is resolved on first use: the in-cluster service
// account when KUBERNETES_SERVICE_HOST is set and no kubeconfig was named,
// otherwise the kubeconfig (explicit path, $KUBECONFIG, or ~/.kube/config).
func NewLiveK8sClientConfigured(opts ...LiveK8sOption) *LiveK8sClient {
	c := &LiveK8sClient{resources: map[string]resourceInfo{}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// CapabilityDiscoveryEnabled permits probes only after a real apply. A dry-run
// does not create workloads, so discovery would target nonexistent services.
func (c *LiveK8sClient) CapabilityDiscoveryEnabled() bool { return !c.dryRun }

func (c *LiveK8sClient) connection() (*clusterConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.override != nil {
		return c.override, nil
	}
	if c.conn != nil {
		return c.conn, nil
	}
	var (
		conn *clusterConn
		err  error
	)
	if c.kubeconfig == "" && inCluster() {
		conn, err = loadInClusterConn()
	} else {
		conn, err = loadKubeconfigConn(c.kubeconfig)
	}
	if err != nil {
		return nil, err
	}
	c.conn = conn
	return conn, nil
}

// Apply creates or updates a K8s resource using server-side apply.
func (c *LiveK8sClient) Apply(ctx context.Context, m Manifest) error {
	body, err := marshalManifest(m)
	if err != nil {
		return fmt.Errorf("marshal manifest %s/%s: %w", m.Kind, m.Name, err)
	}
	if !resourceName.MatchString(m.Name) || len(m.Name) > 253 {
		return fmt.Errorf("apply %s/%s: invalid Kubernetes resource name", m.Kind, m.Name)
	}
	if _, err := qualifiedResourceType(m.APIVersion, m.Kind); err != nil {
		return fmt.Errorf("apply %s/%s: %w", m.Kind, m.Name, err)
	}
	if m.Namespace != "" && (!namespaceName.MatchString(m.Namespace) || len(m.Namespace) > 63) {
		return fmt.Errorf("apply %s/%s: invalid namespace", m.Kind, m.Name)
	}

	// Identity only: never log the body, which can carry configuration.
	logger := log.Ctx(ctx).With().
		Str("kind", m.Kind).
		Str("name", m.Name).
		Str("namespace", m.Namespace).
		Logger()

	if c.dryRun {
		logger.Info().Msg("k8s apply (dry-run)")
		return nil
	}

	conn, err := c.connection()
	if err != nil {
		return fmt.Errorf("apply %s/%s: %w", m.Kind, m.Name, err)
	}
	ns := m.Namespace
	if ns == "" {
		ns = conn.namespace
	}
	target, err := c.objectURL(ctx, conn, m.APIVersion, m.Kind, ns, m.Name)
	if err != nil {
		return fmt.Errorf("apply %s/%s: %w", m.Kind, m.Name, err)
	}
	q := url.Values{"fieldManager": {fieldManager}, "force": {"true"}}
	status, msg, err := conn.do(ctx, http.MethodPatch, target+"?"+q.Encode(), "application/apply-patch+yaml", body)
	if err != nil {
		logger.Error().Err(err).Msg("k8s apply failed")
		return fmt.Errorf("apply %s/%s in namespace %s: %w", m.Kind, m.Name, ns, err)
	}
	if status < 200 || status > 299 {
		logger.Error().Int("status", status).Msg("k8s apply failed")
		return fmt.Errorf("apply %s/%s in namespace %s: HTTP %d: %s", m.Kind, m.Name, ns, status, msg)
	}
	logger.Info().Msg("k8s apply succeeded")
	return nil
}

// Delete removes a K8s resource. A missing object is success (what
// `kubectl delete --ignore-not-found` did). Unlike kubectl it does not wait
// for finalizers; dependents are deleted in the background.
func (c *LiveK8sClient) Delete(ctx context.Context, apiVersion, kind, name, namespace string) error {
	if _, err := qualifiedResourceType(apiVersion, kind); err != nil || !resourceName.MatchString(name) || len(name) > 253 || namespace != "" && (!namespaceName.MatchString(namespace) || len(namespace) > 63) {
		return fmt.Errorf("invalid Kubernetes cleanup resource identity")
	}
	logger := log.Ctx(ctx).With().
		Str("kind", kind).
		Str("name", name).
		Str("namespace", namespace).
		Logger()

	if c.dryRun {
		logger.Info().Msg("k8s delete (dry-run)")
		return nil
	}

	conn, err := c.connection()
	if err != nil {
		return fmt.Errorf("delete %s/%s: %w", kind, name, err)
	}
	ns := namespace
	if ns == "" {
		ns = conn.namespace
	}
	// The recorded API group qualifies the Kind: two installed APIs can share
	// a Kind, so the lookup is by apiVersion, never by Kind alone.
	target, err := c.objectURL(ctx, conn, apiVersion, kind, ns, name)
	if err != nil {
		return fmt.Errorf("delete %s/%s: %w", kind, name, err)
	}
	opts, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Background"})
	status, msg, err := conn.do(ctx, http.MethodDelete, target, "application/json", opts)
	if err != nil {
		logger.Warn().Err(err).Msg("k8s delete failed")
		return fmt.Errorf("delete %s/%s in namespace %s: %w", kind, name, ns, err)
	}
	if status == http.StatusNotFound {
		logger.Info().Msg("k8s delete: already absent")
		return nil
	}
	if status < 200 || status > 299 {
		logger.Warn().Int("status", status).Msg("k8s delete failed")
		return fmt.Errorf("delete %s/%s in namespace %s: HTTP %d: %s", kind, name, ns, status, msg)
	}
	logger.Info().Msg("k8s delete succeeded")
	return nil
}

type resourceInfo struct {
	prefix     string // /api/v1 or /apis/{group}/{version}
	plural     string
	namespaced bool
}

// objectURL maps apiVersion/kind to the namespaced object URL using API
// discovery, caching successful lookups for the life of the client.
func (c *LiveK8sClient) objectURL(ctx context.Context, conn *clusterConn, apiVersion, kind, namespace, name string) (string, error) {
	key := apiVersion + "|" + kind
	c.mu.Lock()
	info, ok := c.resources[key]
	c.mu.Unlock()
	if !ok {
		prefix := "/apis/" + apiVersion
		if !strings.Contains(apiVersion, "/") {
			prefix = "/api/" + apiVersion
		}
		status, body, err := conn.do(ctx, http.MethodGet, conn.base+prefix, "", nil)
		if err != nil {
			return "", fmt.Errorf("discover %s: %w", apiVersion, err)
		}
		if status == http.StatusNotFound {
			return "", fmt.Errorf("the cluster does not serve %s (is its CRD installed?)", apiVersion)
		}
		if status < 200 || status > 299 {
			return "", fmt.Errorf("discover %s: HTTP %d: %s", apiVersion, status, body)
		}
		var list struct {
			Resources []struct {
				Name       string `json:"name"`
				Kind       string `json:"kind"`
				Namespaced bool   `json:"namespaced"`
			} `json:"resources"`
		}
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			return "", fmt.Errorf("discover %s: unreadable reply", apiVersion)
		}
		for _, r := range list.Resources {
			if r.Kind == kind && !strings.Contains(r.Name, "/") {
				info = resourceInfo{prefix: prefix, plural: r.Name, namespaced: r.Namespaced}
				ok = true
				break
			}
		}
		if !ok {
			return "", fmt.Errorf("the cluster does not serve kind %s in %s", kind, apiVersion)
		}
		c.mu.Lock()
		c.resources[key] = info
		c.mu.Unlock()
	}
	if !info.namespaced {
		return "", fmt.Errorf("kind %s is cluster-scoped; the deploy engine only manages namespaced objects", kind)
	}
	if namespace == "" {
		return "", fmt.Errorf("no namespace for %s", kind)
	}
	return conn.base + info.prefix + "/namespaces/" + url.PathEscape(namespace) + "/" + info.plural + "/" + url.PathEscape(name), nil
}

// clusterConn is a resolved API server connection.
type clusterConn struct {
	base      string
	hc        *http.Client
	token     func() (string, error) // re-read per request: projected tokens rotate
	namespace string                 // default namespace for objects that name none
}

// do sends one request. A 2xx reply returns its body; any other reply returns
// only the API server's status message, bounded, never the request body.
func (cn *clusterConn) do(ctx context.Context, method, target, contentType string, body []byte) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return 0, "", fmt.Errorf("build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if cn.token != nil {
		tok, err := cn.token()
		if err != nil {
			return 0, "", err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := cn.hc.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("kubernetes API unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxStatusBody))
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return resp.StatusCode, string(raw), nil
	}
	return resp.StatusCode, statusMessage(raw, resp.Status), nil
}

// statusMessage extracts metav1.Status.message from an error reply, bounded.
func statusMessage(raw []byte, fallback string) string {
	var st struct {
		Message string `json:"message"`
	}
	msg := fallback
	if json.Unmarshal(raw, &st) == nil && st.Message != "" {
		msg = st.Message
	}
	if len(msg) > 500 {
		msg = msg[:500] + "..."
	}
	return msg
}

var resourceName = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9.]*[a-z0-9])?$`)
var namespaceName = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)
var resourceKind = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
var apiVersionName = regexp.MustCompile(`^v[0-9]+(?:alpha[0-9]+|beta[0-9]+)?$`)

// qualifiedResourceType validates apiVersion and kind and returns
// Kind[.group], the form used in messages and by the cleanup inventory.
func qualifiedResourceType(apiVersion, kind string) (string, error) {
	if !resourceKind.MatchString(kind) {
		return "", fmt.Errorf("invalid Kubernetes kind")
	}
	parts := strings.Split(apiVersion, "/")
	if len(parts) == 1 && apiVersionName.MatchString(parts[0]) {
		return kind, nil
	}
	if len(parts) != 2 || !resourceName.MatchString(parts[0]) || len(parts[0]) > 253 || !apiVersionName.MatchString(parts[1]) {
		return "", fmt.Errorf("invalid Kubernetes API version")
	}
	return kind + "." + parts[0], nil
}

// marshalManifest converts a Manifest to YAML bytes.
func marshalManifest(m Manifest) ([]byte, error) {
	if strings.TrimSpace(m.APIVersion) == "" || strings.TrimSpace(m.Kind) == "" || strings.TrimSpace(m.Name) == "" {
		return nil, fmt.Errorf("manifest requires apiVersion, kind, and name")
	}
	doc := map[string]any{
		"apiVersion": m.APIVersion,
		"kind":       m.Kind,
		"metadata": map[string]any{
			"name":      m.Name,
			"namespace": m.Namespace,
		},
	}

	// A module owns exactly the resource identified by Manifest. Never apply
	// undecodable raw content, accept extra documents, or permit an embedded
	// document to redirect an operation to a different kind/name/namespace.
	if strings.TrimSpace(m.YAML) != "" {
		var content map[string]any
		decoder := yaml.NewDecoder(strings.NewReader(m.YAML))
		if err := decoder.Decode(&content); err != nil {
			return nil, fmt.Errorf("invalid manifest YAML: %w", err)
		}
		if content == nil {
			return nil, fmt.Errorf("manifest must be an object")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("manifest must contain exactly one YAML document")
		}
		for field, expected := range map[string]string{"apiVersion": m.APIVersion, "kind": m.Kind} {
			if value, present := content[field]; present {
				if actual, ok := value.(string); !ok || actual != expected {
					return nil, fmt.Errorf("manifest %s does not match declared resource", field)
				}
			}
		}
		metadata := map[string]any{"name": m.Name, "namespace": m.Namespace}
		if value, present := content["metadata"]; present {
			actual, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("manifest metadata must be an object")
			}
			for field, expected := range map[string]string{"name": m.Name, "namespace": m.Namespace} {
				if value, present := actual[field]; present {
					if text, ok := value.(string); !ok || text != expected {
						return nil, fmt.Errorf("manifest metadata.%s does not match declared resource", field)
					}
				}
			}
			for key, value := range actual {
				metadata[key] = value
			}
		}
		// Merge content into doc
		for k, v := range content {
			doc[k] = v
		}
		doc["metadata"] = metadata
	}

	return yaml.Marshal(doc)
}
